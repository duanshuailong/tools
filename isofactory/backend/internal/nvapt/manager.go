package nvapt

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"
)

// State is the lifecycle of a driver/toolkit download.
type State string

const (
	StateIdle        State = "idle"
	StateDownloading State = "downloading"
	StateDone        State = "done"
	StateFailed      State = "failed"
)

// Status is a snapshot of the current/last download.
type Status struct {
	State    State  `json:"state"`
	Driver   string `json:"driver,omitempty"`
	Toolkit  string `json:"toolkit,omitempty"`
	Error    string `json:"error,omitempty"`
	Started  string `json:"started,omitempty"`
	Finished string `json:"finished,omitempty"`
}

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

// Manager runs at most one driver/toolkit download at a time and caches the
// available version lists (refreshed lazily; listing shells out to apt).
//
// Downloads land in a VERSION-KEYED cache (cacheRoot/<driver>__<toolkit>/), not
// a single shared dir — so concurrent builds selecting different versions never
// mix debs, and a version downloaded once is reused. The builder hardlinks the
// selected cache dir into each per-job workspace.
type Manager struct {
	cacheRoot string // <data>/cache/nvidia

	keyringDir string // writable dir for the repo keyring (data dir)

	mu       sync.Mutex
	busy     bool
	st       Status
	log      *syncBuf
	repoMu   sync.Mutex
	repoDone bool

	// keyLocks serialize concurrent downloads of the SAME cache key (one fetches,
	// others wait and reuse); different keys proceed in parallel.
	keyLocks sync.Map // cacheKey -> *sync.Mutex

	verMu    sync.Mutex
	drivers  []DriverVersion
	toolkits []ToolkitVersion
	verAt    time.Time
}

// NewManager returns a Manager caching under cacheRoot, with the repo keyring in
// keyringDir (must be writable, e.g. under the data dir).
func NewManager(cacheRoot, keyringDir string) *Manager {
	return &Manager{
		cacheRoot:  cacheRoot,
		keyringDir: keyringDir,
		st:         Status{State: StateIdle},
		log:        &syncBuf{},
	}
}

// cacheDirFor returns the version-keyed cache directory for a selection.
func (m *Manager) cacheDirFor(sel Selection) string {
	key := sel.Driver + "__" + sel.Toolkit
	return filepath.Join(m.cacheRoot, key)
}

// lockKey returns the per-key mutex, creating it on first use.
func (m *Manager) lockKey(key string) *sync.Mutex {
	v, _ := m.keyLocks.LoadOrStore(key, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// ensureRepo runs EnsureRepo, caching only success (so a transient failure can
// be retried on the next call rather than stuck forever).
func (m *Manager) ensureRepo(ctx context.Context) error {
	m.repoMu.Lock()
	defer m.repoMu.Unlock()
	if m.repoDone {
		return nil
	}
	var buf bytes.Buffer
	if err := EnsureRepo(ctx, m.keyringDir, &buf); err != nil {
		return err
	}
	m.repoDone = true
	return nil
}

// ListVersions returns available driver + toolkit versions, cached for 30 min.
// It sets up the NVIDIA repo on first call.
func (m *Manager) ListVersions(ctx context.Context) ([]DriverVersion, []ToolkitVersion, error) {
	m.verMu.Lock()
	defer m.verMu.Unlock()
	if time.Since(m.verAt) < 30*time.Minute && (len(m.drivers) > 0 || len(m.toolkits) > 0) {
		return m.drivers, m.toolkits, nil
	}
	if err := m.ensureRepo(ctx); err != nil {
		return nil, nil, err
	}
	d, t, err := Versions(ctx)
	if err != nil {
		return nil, nil, err
	}
	m.drivers, m.toolkits, m.verAt = d, t, time.Now()
	return d, t, nil
}

// Start kicks off a background download for the selection.
func (m *Manager) Start(sel Selection) error {
	if sel.Driver == "" && sel.Toolkit == "" {
		return errNothingSelected
	}
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return errBusy
	}
	m.busy = true
	m.log = &syncBuf{}
	m.st = Status{State: StateDownloading, Driver: sel.Driver, Toolkit: sel.Toolkit, Started: now()}
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()
		err := m.ensureRepo(ctx)
		if err == nil {
			err = DownloadTo(ctx, sel, m.cacheDirFor(sel), m.log)
		}
		m.mu.Lock()
		m.st.Finished = now()
		if err != nil {
			m.st.State = StateFailed
			m.st.Error = err.Error()
		} else {
			m.st.State = StateDone
		}
		m.busy = false
		m.mu.Unlock()
	}()
	return nil
}

// DownloadSync ensures the repo then downloads the selection into its
// version-keyed cache dir (reusing it if already populated), and returns that
// dir. Safe for concurrent builds: a per-key lock serializes same-version
// downloads while different versions run in parallel; no shared index is
// written (the builder regenerates the index per workspace).
func (m *Manager) DownloadSync(ctx context.Context, sel Selection, logOut io.Writer) (string, error) {
	if sel.Driver == "" && sel.Toolkit == "" {
		return "", errNothingSelected
	}
	if err := m.ensureRepo(ctx); err != nil {
		return "", err
	}
	dir := m.cacheDirFor(sel)
	lk := m.lockKey(dir)
	lk.Lock()
	defer lk.Unlock()

	if n := countDebs(dir); n > 0 {
		fmt.Fprintf(logOut, "reusing cached NVIDIA stack (%d debs) at %s\n", n, dir)
		return dir, nil
	}
	if err := DownloadTo(ctx, sel, dir, logOut); err != nil {
		return "", err
	}
	return dir, nil
}

// countDebs counts .deb files in a directory.
func countDebs(dir string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.deb"))
	return len(matches)
}

// Status returns the current download status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

// Log returns the current/last download log.
func (m *Manager) Log() string { return m.log.String() }

func now() string { return time.Now().Format("2006-01-02 15:04:05") }
