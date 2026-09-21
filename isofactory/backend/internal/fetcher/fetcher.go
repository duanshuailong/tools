// Package fetcher downloads a source ISO from a mirror when it is missing
// locally, so the server only has to hold the customization template — the
// clean base image is pulled on demand and checksum-verified.
package fetcher

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Source describes a downloadable base ISO.
type Source struct {
	Name    string   // target filename, e.g. ubuntu-24.04.4-live-server-amd64.iso
	URL     string   // primary download URL
	Mirrors []string // fallback URLs, tried in order if the primary fails
	SHA256  string   // expected hex digest, lowercase; "" skips verification
}

// urls returns the primary URL followed by any mirrors, skipping blanks.
func (s Source) urls() []string {
	var out []string
	if s.URL != "" {
		out = append(out, s.URL)
	}
	for _, m := range s.Mirrors {
		if m != "" {
			out = append(out, m)
		}
	}
	return out
}

// The set of downloadable base images now lives in package assets (Catalog);
// this package stays a generic checksum-verifying downloader.

// Progress reports download progress; total may be -1 if unknown.
type Progress struct {
	Downloaded int64
	Total      int64
}

// Fetcher downloads sources into a destination directory.
type Fetcher struct {
	Client *http.Client
}

// New returns a Fetcher with a client suited to large, slow downloads.
func New() *Fetcher {
	return &Fetcher{Client: &http.Client{Timeout: 2 * time.Hour}}
}

// Fetch downloads src into destDir/src.Name, verifies the checksum, and returns
// the final path. It writes to a .part file and renames on success, so an
// interrupted download never looks like a complete ISO. progressFn, if non-nil,
// is called periodically. If the file already exists with the right checksum it
// is reused without downloading.
func (f *Fetcher) Fetch(src Source, destDir string, progressFn func(Progress)) (string, error) {
	final := filepath.Join(destDir, src.Name)

	// Reuse an existing, verified file.
	if src.SHA256 != "" {
		if sum, err := fileSHA256(final); err == nil && sum == src.SHA256 {
			return final, nil
		}
	} else if _, err := os.Stat(final); err == nil {
		return final, nil
	}

	candidates := src.urls()
	if len(candidates) == 0 {
		return "", fmt.Errorf("no download URL configured for %s", src.Name)
	}

	var lastErr error
	for _, url := range candidates {
		if err := f.downloadOne(url, final, src.SHA256, progressFn); err != nil {
			lastErr = err
			continue // try the next mirror
		}
		return final, nil
	}
	return "", fmt.Errorf("all sources failed for %s (last error: %w)", src.Name, lastErr)
}

// downloadOne downloads a single URL into final via a .part file, verifying the
// checksum if given. It cleans up the .part file on any failure so a failed
// attempt never leaves a partial or renames to final.
func (f *Fetcher) downloadOne(url, final, wantSHA string, progressFn func(Progress)) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := f.client().Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected status %s", url, resp.Status)
	}

	part := final + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}

	h := sha256.New()
	pw := &progressWriter{total: resp.ContentLength, fn: progressFn}
	// Tee download into the file and the hasher; count bytes via progressWriter.
	_, copyErr := io.Copy(io.MultiWriter(out, h, pw), resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		os.Remove(part)
		return fmt.Errorf("download body: %w", copyErr)
	}
	if closeErr != nil {
		os.Remove(part)
		return closeErr
	}

	if wantSHA != "" {
		got := hex.EncodeToString(h.Sum(nil))
		if got != wantSHA {
			os.Remove(part)
			return fmt.Errorf("checksum mismatch: got %s, want %s", got, wantSHA)
		}
	}

	if err := os.Rename(part, final); err != nil {
		os.Remove(part)
		return err
	}
	return nil
}

func (f *Fetcher) client() *http.Client {
	if f.Client != nil {
		return f.Client
	}
	return http.DefaultClient
}

// progressWriter counts bytes and throttles progress callbacks.
type progressWriter struct {
	total   int64
	written int64
	last    int64
	fn      func(Progress)
	lastAt  atomic.Int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n := len(b)
	p.written += int64(n)
	if p.fn != nil {
		now := time.Now().UnixMilli()
		// Report at most every 500ms to avoid flooding.
		if now-p.lastAt.Load() >= 500 {
			p.lastAt.Store(now)
			p.fn(Progress{Downloaded: p.written, Total: p.total})
		}
	}
	p.last = p.written
	return n, nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
