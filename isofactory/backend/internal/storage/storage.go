// Package storage persists build-job metadata, logs, and produced ISOs to disk
// so history and downloads survive a backend restart.
//
// Layout under the base dir:
//
//	<base>/jobs/<id>.json   — job metadata (status, options, timestamps, iso path)
//	<base>/jobs/<id>.log    — accumulated build log
//	<base>/isos/<id>.iso    — the produced ISO for that job
//
// Each job's ISO gets its own path so a later build cannot overwrite an earlier
// job's downloadable artifact (pack.sh always writes template/new-ubuntu.iso,
// which we relocate here per job).
package storage

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Record is the on-disk form of a job. It mirrors the queue's Job but is a
// plain serializable struct (no live buffers), decoupling disk format from the
// in-memory type.
type Record struct {
	ID            string `json:"id"`
	OS            string `json:"os,omitempty"`
	SystemVersion string `json:"system_version,omitempty"`
	IncludeNVIDIA bool   `json:"include_nvidia"`
	NVIDIAVersion string `json:"nvidia_version,omitempty"`
	CUDAVersion   string `json:"cuda_version,omitempty"`
	IncludeOFED   bool   `json:"include_ofed"`
	OFEDVersion   string `json:"ofed_version,omitempty"`
	OutputName    string `json:"output_name,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	ISOPath       string `json:"iso_path,omitempty"`
	Queued        string `json:"queued"`
	Started       string `json:"started,omitempty"`
	Finished      string `json:"finished,omitempty"`
}

// Store reads and writes job records under a base directory.
type Store struct {
	dir    string // <base>/jobs
	isoDir string // <base>/isos
}

// New creates a Store rooted at base, creating the jobs and isos directories.
func New(base string) (*Store, error) {
	dir := filepath.Join(base, "jobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	isoDir := filepath.Join(base, "isos")
	if err := os.MkdirAll(isoDir, 0o755); err != nil {
		return nil, err
	}
	return &Store{dir: dir, isoDir: isoDir}, nil
}

// ISOPathFor returns the canonical per-job ISO path (may not exist yet).
func (s *Store) ISOPathFor(id string) string {
	return filepath.Join(s.isoDir, id+".iso")
}

// StoreISO moves the freshly built ISO at srcPath to the per-job path and
// returns it. A rename is used when possible; if src and dest are on different
// filesystems it falls back to copy+remove.
func (s *Store) StoreISO(id, srcPath string) (string, error) {
	dest := s.ISOPathFor(id)
	if err := os.Rename(srcPath, dest); err == nil {
		return dest, nil
	}
	// Cross-device or other rename failure: copy then remove the source.
	if err := copyFile(srcPath, dest); err != nil {
		return "", err
	}
	_ = os.Remove(srcPath)
	return dest, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(dst)
		return err
	}
	return out.Close()
}

// SaveRecord writes (or overwrites) a job's metadata atomically.
func (s *Store) SaveRecord(r Record) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, r.ID+".json"), data)
}

// AppendLog appends a chunk to a job's log file.
func (s *Store) AppendLog(id, chunk string) error {
	f, err := os.OpenFile(filepath.Join(s.dir, id+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(chunk)
	return err
}

// ReadLog returns a job's full log, or "" if none exists yet.
func (s *Store) ReadLog(id string) string {
	data, err := os.ReadFile(filepath.Join(s.dir, id+".log"))
	if err != nil {
		return ""
	}
	return string(data)
}

// LoadAll returns every persisted record, newest ID first (IDs are timestamp-
// prefixed, so lexical desc ≈ chronological desc).
func (s *Store) LoadAll() ([]Record, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))

	out := make([]Record, 0, len(names))
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(s.dir, n))
		if err != nil {
			continue // skip unreadable record rather than fail the whole load
		}
		var r Record
		if json.Unmarshal(data, &r) == nil {
			out = append(out, r)
		}
	}
	return out, nil
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
