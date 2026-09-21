// Package doca provides DOCA-OFED download support — the modern replacement for
// the frozen MLNX_OFED .tgz line.
//
// MLNX_OFED (content.mellanox.com/ofed) stopped at 24.10; newer OFED ships only
// inside NVIDIA DOCA. DOCA is a standard apt repo, and its `doca-ofed`
// metapackage pulls the current OFED stack (e.g. DOCA 3.5.0 → OFED 26.07). This
// package sets up the DOCA apt repo (deploy-time, as root) and downloads the
// doca-ofed dependency closure into the offline repo so it bakes into the ISO.
//
// Linux-only: shells out to apt. Like package nvapt, in production the repo is
// configured at deploy time and the service reads it read-only.
package doca

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultVersion is the DOCA release whose repo is configured at deploy time.
// 3.5.0 ships OFED 26.07 for ubuntu24.04. Verified reachable 2026-09-20.
const DefaultVersion = "3.5.0"

func repoBase(version string) string {
	return fmt.Sprintf("https://linux.mellanox.com/public/repo/doca/%s/ubuntu24.04/x86_64", version)
}

const (
	keyringPath = "/usr/share/keyrings/isofactory-doca.gpg"
	sourcePath  = "/etc/apt/sources.list.d/isofactory-doca.list"
)

var errBusy = fmt.Errorf("另一个 DOCA-OFED 下载正在进行中，请稍候")

// Available reports whether apt tooling is present (Linux host).
func Available() bool {
	_, e1 := exec.LookPath("apt-get")
	_, e2 := exec.LookPath("apt-cache")
	return e1 == nil && e2 == nil
}

// repoReady reports whether doca-ofed is already visible via apt (repo set up at
// deploy time). Normal production path — service reads read-only.
func repoReady(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "apt-cache", "madison", "doca-ofed").Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// EnsureRepo makes the DOCA apt repo available. Fast-paths to no-op when the
// repo is already visible (deploy-time setup). Otherwise installs the keyring +
// source (needs root/writable /etc). keyringDir is a writable fallback dir.
func EnsureRepo(ctx context.Context, version, keyringDir string, log io.Writer) error {
	if !Available() {
		return fmt.Errorf("apt not available on this host")
	}
	if repoReady(ctx) {
		fmt.Fprintln(log, "DOCA repo already available")
		return nil
	}
	if err := os.MkdirAll(keyringDir, 0o755); err != nil {
		return err
	}
	kp := filepath.Join(keyringDir, "doca.gpg")
	if _, err := os.Stat(kp); err != nil {
		fmt.Fprintln(log, "installing DOCA repo keyring")
		// The DOCA repo ships an already-dearmored binary keyring (doca_keyring.gpg),
		// so download it straight to the keyring path — no gpg --dearmor needed.
		key, err := exec.CommandContext(ctx, "curl", "-fsSL", repoBase(version)+"/doca_keyring.gpg").Output()
		if err != nil {
			return fmt.Errorf("fetch DOCA keyring: %w", err)
		}
		if err := os.WriteFile(kp, key, 0o644); err != nil {
			return fmt.Errorf("write DOCA keyring: %w", err)
		}
	}
	line := fmt.Sprintf("deb [signed-by=%s] %s/ /\n", kp, repoBase(version))
	if cur, _ := os.ReadFile(sourcePath); string(cur) != line {
		if err := os.WriteFile(sourcePath, []byte(line), 0o644); err != nil {
			return fmt.Errorf("DOCA 源未就绪且无法写入 %s（服务非 root）。请在服务器以 root 跑 deploy/setup.sh 配置 DOCA 源: %w", sourcePath, err)
		}
	}
	fmt.Fprintln(log, "refreshing DOCA package list")
	upd := exec.CommandContext(ctx, "apt-get", "update",
		"-o", "Dir::Etc::sourcelist=sources.list.d/isofactory-doca.list",
		"-o", "Dir::Etc::sourceparts=-", "-o", "APT::Get::List-Cleanup=0")
	upd.Stdout, upd.Stderr = log, log
	if err := upd.Run(); err != nil {
		return fmt.Errorf("apt-get update (doca): %w", err)
	}
	return nil
}

// OFEDVersion returns the doca-ofed package version visible via apt (best-effort;
// "" if unknown). Used to show what OFED the DOCA repo provides.
func OFEDVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "apt-cache", "madison", "doca-ofed").Output()
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(out))
	if sc.Scan() {
		parts := strings.Split(sc.Text(), "|")
		if len(parts) >= 2 {
			return strings.TrimSpace(parts[1])
		}
	}
	return ""
}

// DownloadTo resolves the doca-ofed dependency closure and downloads all debs
// into destDir. No index is written here — the builder regenerates the offline
// index once per workspace after assembly. EnsureRepo first.
func DownloadTo(ctx context.Context, destDir string, log io.Writer) error {
	if !Available() {
		return fmt.Errorf("apt not available on this host")
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	fmt.Fprintln(log, "resolving doca-ofed dependency closure")
	closure, err := resolveClosure(ctx, []string{"doca-ofed"})
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "closure has %d packages; downloading into %s\n", len(closure), destDir)
	cmd := exec.CommandContext(ctx, "apt-get", append([]string{"download"}, closure...)...)
	cmd.Dir = destDir
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(log, "warning: apt-get download reported errors (some packages may be unavailable): %v\n", err)
	}
	return nil
}

func resolveClosure(ctx context.Context, pkgs []string) ([]string, error) {
	args := append([]string{"depends", "--recurse", "--no-recommends", "--no-suggests",
		"--no-conflicts", "--no-breaks", "--no-replaces", "--no-enhances", "--no-pre-depends"}, pkgs...)
	out, err := exec.CommandContext(ctx, "apt-cache", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("apt-cache depends: %w", err)
	}
	set := map[string]struct{}{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == ' ' {
			continue
		}
		name := strings.TrimSpace(line)
		if strings.HasPrefix(name, "<") {
			continue
		}
		if i := strings.IndexByte(name, ':'); i >= 0 {
			name = name[:i]
		}
		if name != "" {
			set[name] = struct{}{}
		}
	}
	out2 := make([]string, 0, len(set))
	for n := range set {
		out2 = append(out2, n)
	}
	sort.Strings(out2)
	return out2, nil
}

func generateIndex(ctx context.Context, debsRoot string, log io.Writer) error {
	fmt.Fprintln(log, "regenerating offline apt index")
	scan := exec.CommandContext(ctx, "dpkg-scanpackages", "-m", ".", "/dev/null")
	scan.Dir = debsRoot
	data, err := scan.Output()
	if err != nil {
		return fmt.Errorf("dpkg-scanpackages: %w", err)
	}
	if err := os.WriteFile(filepath.Join(debsRoot, "Packages"), data, 0o644); err != nil {
		return err
	}
	var gz bytes.Buffer
	g := exec.CommandContext(ctx, "gzip", "-9c")
	g.Stdin = bytes.NewReader(data)
	g.Stdout = &gz
	if err := g.Run(); err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	return os.WriteFile(filepath.Join(debsRoot, "Packages.gz"), gz.Bytes(), 0o644)
}
