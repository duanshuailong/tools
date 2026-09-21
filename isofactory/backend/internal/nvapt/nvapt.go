// Package nvapt provides independent NVIDIA driver + CUDA toolkit version
// selection and download via NVIDIA's official CUDA apt repository.
//
// Unlike the bundled .run installer (which locks a driver to a CUDA version),
// the apt repo ships `cuda-drivers-<branch>` / `cuda-drivers` and
// `cuda-toolkit-<maj>-<min>` as independently versioned packages, so a driver
// and a CUDA toolkit can be chosen freely. Selected packages (with their full
// dependency closure) are downloaded into template/debs/nvidia-apt/ and folded
// into the offline apt index, so they install offline at deploy time via apt
// (rather than the .run split/reassemble path).
//
// Linux-only: shells out to apt. The NVIDIA CUDA repo is added as a dedicated,
// additive source (official repo — legitimate on a build host); it never
// rewrites the system's existing sources.
package nvapt

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	repoBase = "https://developer.download.nvidia.cn/compute/cuda/repos/ubuntu2404/x86_64"
	keyURL   = repoBase + "/3bf863cc.pub"
	// sourcePath is under /etc/apt/sources.list.d — carved out as writable in the
	// systemd unit (ReadWritePaths). The keyring lives in a writable data dir
	// (keyringPath, set per-Manager) rather than /usr, which stays read-only.
	sourcePath = "/etc/apt/sources.list.d/isofactory-nvidia-cuda.list"
)

var (
	errBusy            = fmt.Errorf("另一个驱动/CUDA 下载正在进行中，请稍候")
	errNothingSelected = fmt.Errorf("请至少选择一个驱动或 CUDA 版本")
)

// Available reports whether apt tooling is present (Linux host).
func Available() bool {
	_, e1 := exec.LookPath("apt-get")
	_, e2 := exec.LookPath("apt-cache")
	return e1 == nil && e2 == nil
}

// repoReady reports whether the NVIDIA CUDA repo is already usable — i.e. its
// packages are visible via apt (set up at deploy time by deploy/setup.sh). This
// is the normal path in production: the service reads the repo read-only and
// never writes to system apt directories.
func repoReady(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "apt-cache", "madison", "cuda-drivers").Output()
	return err == nil && len(bytes.TrimSpace(out)) > 0
}

// EnsureRepo makes the NVIDIA CUDA repo available. In production the repo is
// installed at deploy time (by deploy/setup.sh, as root), so this fast-paths to
// a no-op when the repo is already visible. Otherwise — e.g. a root CLI run — it
// installs the keyring into keyringDir + a dedicated source list and refreshes
// that list. When neither holds (non-root, repo absent), it returns a clear
// error pointing at the deploy-time setup step.
func EnsureRepo(ctx context.Context, keyringDir string, log io.Writer) error {
	if !Available() {
		return fmt.Errorf("apt not available on this host")
	}
	if repoReady(ctx) {
		fmt.Fprintln(log, "NVIDIA CUDA repo already available")
		return nil
	}
	if err := os.MkdirAll(keyringDir, 0o755); err != nil {
		return err
	}
	keyringPath := filepath.Join(keyringDir, "nvidia-cuda.gpg")
	if _, err := os.Stat(keyringPath); err != nil {
		fmt.Fprintln(log, "installing NVIDIA CUDA repo keyring")
		pub, err := httpGetTo(ctx)
		if err != nil {
			return err
		}
		gpg := exec.CommandContext(ctx, "gpg", "--dearmor", "-o", keyringPath)
		gpg.Stdin = bytes.NewReader(pub)
		if out, err := gpg.CombinedOutput(); err != nil {
			return fmt.Errorf("dearmor key: %v: %s", err, out)
		}
	}
	line := fmt.Sprintf("deb [signed-by=%s] %s/ /\n", keyringPath, repoBase)
	if cur, _ := os.ReadFile(sourcePath); string(cur) != line {
		if err := os.WriteFile(sourcePath, []byte(line), 0o644); err != nil {
			return fmt.Errorf("NVIDIA CUDA 源未就绪，且无法写入 %s（服务以非 root 运行）。"+
				"请在服务器上以 root 执行 deploy/setup.sh（或其中的 NVIDIA 源配置段）以配置该源: %w", sourcePath, err)
		}
	}
	fmt.Fprintln(log, "refreshing NVIDIA CUDA package list")
	// Scope the update to just our list to avoid a full system apt update.
	upd := exec.CommandContext(ctx, "apt-get", "update",
		"-o", "Dir::Etc::sourcelist=sources.list.d/isofactory-nvidia-cuda.list",
		"-o", "Dir::Etc::sourceparts=-",
		"-o", "APT::Get::List-Cleanup=0")
	upd.Stdout = log
	upd.Stderr = log
	if err := upd.Run(); err != nil {
		return fmt.Errorf("apt-get update (nvidia): %w", err)
	}
	return nil
}

var reMadison = regexp.MustCompile(`\|\s*([0-9][^|]*?)\s*\|`)

// madisonVersions returns the available versions for a package via apt-cache
// madison (newest first, deduped by the version's leading numeric token).
func madisonVersions(ctx context.Context, pkg string) []string {
	out, err := exec.CommandContext(ctx, "apt-cache", "madison", pkg).Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var vers []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := reMadison.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		v := strings.TrimSpace(m[1])
		if !seen[v] {
			seen[v] = true
			vers = append(vers, v)
		}
	}
	return vers
}

// DriverVersion is an available NVIDIA driver (the cuda-drivers metapackage
// version, e.g. 580.65.06-1).
type DriverVersion struct {
	Version string `json:"version"`
}

// ToolkitVersion is an available CUDA toolkit (metapackage cuda-toolkit-<maj>-<min>).
type ToolkitVersion struct {
	Package string `json:"package"` // cuda-toolkit-13-2
	Label   string `json:"label"`   // 13.2
	Version string `json:"version"` // 13.2.2-1
}

var reToolkitPkg = regexp.MustCompile(`^cuda-toolkit-(\d+)-(\d+)$`)

// Versions lists available driver + toolkit versions. Requires EnsureRepo first.
func Versions(ctx context.Context) ([]DriverVersion, []ToolkitVersion, error) {
	if !Available() {
		return nil, nil, fmt.Errorf("apt not available")
	}
	// Drivers: the `cuda-drivers` metapackage's versions == driver versions.
	var drivers []DriverVersion
	for _, v := range madisonVersions(ctx, "cuda-drivers") {
		drivers = append(drivers, DriverVersion{Version: v})
	}
	// Toolkits: enumerate cuda-toolkit-<maj>-<min> packages from the package list.
	var toolkits []ToolkitVersion
	pkgs := listToolkitPackages(ctx)
	for _, p := range pkgs {
		vs := madisonVersions(ctx, p)
		if len(vs) == 0 {
			continue
		}
		m := reToolkitPkg.FindStringSubmatch(p)
		label := p
		if m != nil {
			label = m[1] + "." + m[2]
		}
		toolkits = append(toolkits, ToolkitVersion{Package: p, Label: label, Version: vs[0]})
	}
	return drivers, toolkits, nil
}

// listToolkitPackages returns cuda-toolkit-<maj>-<min> package names, newest first.
func listToolkitPackages(ctx context.Context) []string {
	out, err := exec.CommandContext(ctx, "apt-cache", "pkgnames", "cuda-toolkit-").Output()
	if err != nil {
		return nil
	}
	var pkgs []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if reToolkitPkg.MatchString(p) {
			pkgs = append(pkgs, p)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(pkgs)))
	return pkgs
}

func httpGetTo(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "curl", "-fsSL", keyURL)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("fetch NVIDIA repo key: %w", err)
	}
	return out, nil
}
