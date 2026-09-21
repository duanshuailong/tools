package toolpkgs

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"sync"
	"time"
)

// State is the status of a group's download.
type State string

const (
	StateIdle        State = "idle"        // never downloaded this session
	StateDownloading State = "downloading" // in progress
	StateDone        State = "done"
	StateFailed      State = "failed"
)

// Status is a snapshot of a group's download state.
type Status struct {
	Group    string `json:"group"`
	State    State  `json:"state"`
	Error    string `json:"error,omitempty"`
	Started  string `json:"started,omitempty"`
	Finished string `json:"finished,omitempty"`
	DebCount int    `json:"deb_count"` // .deb files currently in the group dir
}

// syncBuf is a concurrency-safe log buffer.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}
func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

// Manager runs at most one tool-package download at a time (they hammer apt and
// write into a shared debs tree) and tracks per-group status + logs.
type Manager struct {
	d        *Downloader
	mu       sync.Mutex
	st       map[string]*Status
	log      map[string]*syncBuf
	busy     bool
	keyLocks sync.Map // group name -> *sync.Mutex
}

// NewManager returns a Manager writing into debsDir.
func NewManager(debsDir string) *Manager {
	return &Manager{
		d:   &Downloader{DebsDir: debsDir},
		st:  map[string]*Status{},
		log: map[string]*syncBuf{},
	}
}

// Start kicks off a download for the named group. It returns an error if the
// group is unknown or a download is already running.
func (m *Manager) Start(name string) error {
	g, ok := GroupByName(name)
	if !ok {
		return errUnknownGroup(name)
	}
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return errBusy
	}
	m.busy = true
	buf := &syncBuf{}
	m.log[name] = buf
	m.st[name] = &Status{Group: name, State: StateDownloading, Started: now()}
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		err := m.d.Download(ctx, g, buf)
		m.mu.Lock()
		s := m.st[name]
		s.Finished = now()
		s.DebCount = countDebs(m.d.DebsDir, name)
		if err != nil {
			s.State = StateFailed
			s.Error = err.Error()
		} else {
			s.State = StateDone
		}
		m.busy = false
		m.mu.Unlock()
	}()
	return nil
}

// Status returns a snapshot for all groups (merging live state with on-disk deb
// counts for groups never started this session).
func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Status
	for _, g := range DefaultGroups() {
		if s, ok := m.st[g.Name]; ok {
			cp := *s
			cp.DebCount = countDebs(m.d.DebsDir, g.Name)
			out = append(out, cp)
		} else {
			out = append(out, Status{Group: g.Name, State: StateIdle, DebCount: countDebs(m.d.DebsDir, g.Name)})
		}
	}
	return out
}

// Log returns the accumulated log for a group.
func (m *Manager) Log(name string) string {
	m.mu.Lock()
	buf := m.log[name]
	m.mu.Unlock()
	if buf == nil {
		return ""
	}
	return buf.String()
}

// EnsureGroupsSync downloads the named groups into their cache dirs (skipping
// any already populated) and returns the per-group cache directories, so the
// builder can hardlink them into a per-job workspace. A per-key lock serializes
// concurrent same-group downloads while different groups proceed in parallel.
func (m *Manager) EnsureGroupsSync(ctx context.Context, names []string, log io.Writer) ([]string, error) {
	var dirs []string
	for _, name := range names {
		g, ok := GroupByName(name)
		if !ok {
			continue // unknown group name — ignore rather than fail the build
		}
		dir := filepath.Join(m.d.DebsDir, name)
		lk := m.lockKey(name)
		lk.Lock()
		if countDebs(m.d.DebsDir, name) == 0 {
			if err := m.d.Download(ctx, g, log); err != nil {
				lk.Unlock()
				return nil, err
			}
		}
		lk.Unlock()
		dirs = append(dirs, dir)
	}
	return dirs, nil
}

// lockKey returns the per-group mutex, creating it on first use.
func (m *Manager) lockKey(key string) *sync.Mutex {
	v, _ := m.keyLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

func now() string { return time.Now().Format("2006-01-02 15:04:05") }
