package doca

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"sync"
	"time"
)

// State is the lifecycle of a DOCA-OFED download.
type State string

const (
	StateIdle        State = "idle"
	StateDownloading State = "downloading"
	StateDone        State = "done"
	StateFailed      State = "failed"
)

// Status snapshots the current/last download.
type Status struct {
	State       State  `json:"state"`
	Version     string `json:"version,omitempty"`      // DOCA version
	OFEDVersion string `json:"ofed_version,omitempty"` // doca-ofed pkg version
	Error       string `json:"error,omitempty"`
	Started     string `json:"started,omitempty"`
	Finished    string `json:"finished,omitempty"`
	DebCount    int    `json:"deb_count"`
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

// Manager runs one DOCA-OFED download at a time into a version-keyed cache dir
// (cacheRoot/<version>/), so the builder can hardlink it into per-job
// workspaces without concurrent builds mixing versions.
type Manager struct {
	version    string
	cacheRoot  string
	keyringDir string

	mu       sync.Mutex
	busy     bool
	st       Status
	log      *syncBuf
	repoMu   sync.Mutex
	repoDone bool
}

// NewManager returns a Manager for the default DOCA version, caching under cacheRoot.
func NewManager(cacheRoot, keyringDir string) *Manager {
	return &Manager{
		version:    DefaultVersion,
		cacheRoot:  cacheRoot,
		keyringDir: keyringDir,
		st:         Status{State: StateIdle, Version: DefaultVersion},
		log:        &syncBuf{},
	}
}

// cacheDir returns the version-keyed cache directory.
func (m *Manager) cacheDir() string { return filepath.Join(m.cacheRoot, m.version) }

func (m *Manager) ensureRepo(ctx context.Context) error {
	m.repoMu.Lock()
	defer m.repoMu.Unlock()
	if m.repoDone {
		return nil
	}
	var buf bytes.Buffer
	if err := EnsureRepo(ctx, m.version, m.keyringDir, &buf); err != nil {
		return err
	}
	m.repoDone = true
	return nil
}

// Info returns availability + the DOCA/OFED version the repo provides.
func (m *Manager) Info(ctx context.Context) map[string]any {
	info := map[string]any{"available": Available(), "version": m.version}
	if !Available() {
		return info
	}
	if err := m.ensureRepo(ctx); err != nil {
		info["error"] = err.Error()
		return info
	}
	info["ofed_version"] = OFEDVersion(ctx)
	return info
}

// Status returns the current status (with live deb count).
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.st
	s.DebCount = countDebs(m.cacheDir())
	return s
}

// Log returns the current/last download log.
func (m *Manager) Log() string { return m.log.String() }

// Start kicks off a background DOCA-OFED download.
func (m *Manager) Start() error {
	m.mu.Lock()
	if m.busy {
		m.mu.Unlock()
		return errBusy
	}
	m.busy = true
	m.log = &syncBuf{}
	m.st = Status{State: StateDownloading, Version: m.version, Started: now()}
	m.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
		defer cancel()
		err := m.ensureRepo(ctx)
		if err == nil {
			err = DownloadTo(ctx, m.cacheDir(), m.log)
		}
		m.mu.Lock()
		m.st.Finished = now()
		m.st.OFEDVersion = OFEDVersion(ctx)
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

// DownloadSync ensures the repo then downloads into the version-keyed cache dir
// (reusing it if populated) and returns that dir. Concurrent builds hardlink
// from it; no shared index is written (builder regenerates per workspace).
func (m *Manager) DownloadSync(ctx context.Context, logOut io.Writer) (string, error) {
	if err := m.ensureRepo(ctx); err != nil {
		return "", err
	}
	dir := m.cacheDir()
	if countDebs(dir) > 0 {
		return dir, nil // already present, reuse
	}
	if err := DownloadTo(ctx, dir, logOut); err != nil {
		return "", err
	}
	return dir, nil
}

func countDebs(dir string) int {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.deb"))
	return len(matches)
}

func now() string { return time.Now().Format("2006-01-02 15:04:05") }
