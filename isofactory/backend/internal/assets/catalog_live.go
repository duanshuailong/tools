package assets

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Live catalog discovery. Ubuntu's mirror directory listing is scrapable, so we
// discover available base-image versions (and their checksums) at runtime and
// merge them over the curated fallback — this keeps the version list current
// (e.g. new point releases, 26.04) without code changes. CUDA/OFED have no
// browsable index, so those remain curated (refreshed to current versions).

const ubuntuReleasesBase = "https://mirrors.aliyun.com/ubuntu-releases"
const ubuntuReleasesNJU = "https://mirrors.nju.edu.cn/ubuntu-releases"

// The mirror has "series" dirs like `24.04/` and `26.04/` (two version
// components). Each series dir physically holds all its point-release ISOs, and
// its SHA256SUMS lists them (e.g. 24.04 → 24.04.3/.4/.5). Point-release dirs
// like `24.04.4/` also exist but just mirror the same SHA256SUMS, so we scan
// only series dirs to avoid duplicates. The href slash is optional.
var reSeriesDir = regexp.MustCompile(`href="(2[2-9]\.\d{2})/?"`)
var reSHALine = regexp.MustCompile(`^([0-9a-f]{64})\s+\*?(ubuntu-([\d.]+)-live-server-amd64\.iso)$`)

// RefreshUbuntu discovers Ubuntu live-server images from the mirror and returns
// them as ImageReleases (with checksums). The version is taken from each ISO's
// filename (not the dir name), and the download URL points at the series dir
// where the file physically lives. Bounded and time-limited; on any error it
// returns what it has so far.
func RefreshUbuntu(ctx context.Context, client *http.Client) ([]ImageRelease, error) {
	idx, err := httpGet(ctx, client, ubuntuReleasesBase+"/")
	if err != nil {
		return nil, err
	}
	// Collect distinct series dirs, newest first.
	seenSeries := map[string]bool{}
	var series []string
	for _, m := range reSeriesDir.FindAllStringSubmatch(string(idx), -1) {
		if !seenSeries[m[1]] {
			seenSeries[m[1]] = true
			series = append(series, m[1])
		}
	}
	sort.Sort(sort.Reverse(byVersion(series)))
	if len(series) > 8 {
		series = series[:8]
	}

	var out []ImageRelease
	seenVer := map[string]bool{} // dedupe by ISO version across dirs
	for _, s := range series {
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		default:
		}
		sums, err := httpGet(ctx, client, fmt.Sprintf("%s/%s/SHA256SUMS", ubuntuReleasesBase, s))
		if err != nil {
			continue // series dir without a readable SHA256SUMS — skip
		}
		sc := bufio.NewScanner(bytes.NewReader(sums))
		for sc.Scan() {
			m := reSHALine.FindStringSubmatch(strings.TrimSpace(sc.Text()))
			if m == nil {
				continue
			}
			sha, name, ver := m[1], m[2], m[3]
			if seenVer[ver] {
				continue
			}
			seenVer[ver] = true
			out = append(out, ImageRelease{
				OS: "ubuntu", Version: ver, Name: name,
				URL:     fmt.Sprintf("%s/%s/%s", ubuntuReleasesBase, s, name),
				Mirrors: []string{fmt.Sprintf("%s/%s/%s", ubuntuReleasesNJU, s, name)},
				SHA256:  sha,
			})
		}
	}
	return out, nil
}

func httpGet(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var buf bytes.Buffer
	_, err = buf.ReadFrom(resp.Body)
	return buf.Bytes(), err
}

// byVersion sorts dotted version strings numerically (2.10 > 2.9).
type byVersion []string

func (b byVersion) Len() int      { return len(b) }
func (b byVersion) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byVersion) Less(i, j int) bool {
	ai, aj := strings.Split(b[i], "."), strings.Split(b[j], ".")
	for k := 0; k < len(ai) && k < len(aj); k++ {
		x, y := atoi(ai[k]), atoi(aj[k])
		if x != y {
			return x < y
		}
	}
	return len(ai) < len(aj)
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return n
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// StartAutoRefresh periodically refreshes the store's Ubuntu image list from the
// mirror (immediately, then every interval). It merges discovered images over
// the curated ones; failures are ignored (curated list stays in effect). Returns
// a stop function.
func (s *Store) StartAutoRefresh(interval time.Duration) func() {
	client := &http.Client{Timeout: 30 * time.Second}
	stop := make(chan struct{})
	refresh := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		imgs, err := RefreshUbuntu(ctx, client)
		if err != nil || len(imgs) == 0 {
			return
		}
		s.mergeImages(imgs)
	}
	go func() {
		refresh()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				refresh()
			}
		}
	}()
	return func() { close(stop) }
}

// mergeImages replaces the catalog's image list with the discovered set, keeping
// any curated OS entries not covered by discovery. Guarded by the store lock.
func (s *Store) mergeImages(discovered []ImageRelease) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Keep curated non-ubuntu images; ubuntu comes entirely from discovery.
	var merged []ImageRelease
	for _, im := range s.catalog.Images {
		if im.OS != "ubuntu" {
			merged = append(merged, im)
		}
	}
	merged = append(merged, discovered...)
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].OS != merged[j].OS {
			return merged[i].OS < merged[j].OS
		}
		return byVersion([]string{merged[j].Version, merged[i].Version}).Less(0, 1)
	})
	s.catalog.Images = merged
}
