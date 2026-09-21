// Package toolpkgs downloads apt tool packages (with full dependency closure)
// into the template's debs/<group>/ directory and regenerates the offline apt
// index, so common utilities are baked into the produced ISO and installable
// offline at deploy time.
//
// It shells out to apt on the host (Linux only): the recursive dependency
// closure is resolved by name with `apt-cache depends --recurse`, then each
// package is fetched with `apt-get download`. Resolving the full closure (not
// just this host's missing delta) means the debs are complete for a clean
// target machine, matching template/README.md §4's "clean same-version" intent.
package toolpkgs

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

// Group is a named set of top-level apt packages bundled together.
type Group struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Packages    []string `json:"packages"`
}

// DefaultGroups are the tool-package groups, drawn from template/README.md §4.
func DefaultGroups() []Group {
	return []Group{
		{Name: "base", Title: "编译工具链", Description: "build-essential/gcc/make/dkms 等",
			Packages: []string{"build-essential", "make", "gcc", "debhelper", "bison", "flex", "dkms", "pkg-config", "libtool", "gfortran", "cmake", "automake", "autoconf"}},
		{Name: "sysmon", Title: "监控排障", Description: "htop/iotop/sysstat/strace/gdb 等",
			Packages: []string{"htop", "iotop", "sysstat", "lsof", "strace", "ltrace", "gdb", "stress-ng", "numactl"}},
		{Name: "storage", Title: "存储/RAID", Description: "mdadm/lvm2/nvme-cli/smartmontools 等",
			Packages: []string{"mdadm", "lvm2", "parted", "gdisk", "xfsprogs", "e2fsprogs", "nvme-cli", "smartmontools", "hdparm", "multipath-tools", "lsscsi", "sg3-utils"}},
		{Name: "net-tools", Title: "网络工具", Description: "net-tools/tcpdump/mtr/iperf3/nmap 等",
			Packages: []string{"net-tools", "dnsutils", "tcpdump", "conntrack", "socat", "traceroute", "mtr", "iperf3", "nmap", "netcat-openbsd", "bridge-utils", "vlan", "ethtool"}},
		{Name: "hardware", Title: "硬件/BMC", Description: "dmidecode/ipmitool/pciutils/lm-sensors 等",
			Packages: []string{"dmidecode", "ipmitool", "hwdata", "pciutils", "usbutils", "lm-sensors", "freeipmi-tools"}},
		{Name: "term", Title: "终端/传输", Description: "tmux/screen/curl/wget/jq/rsync 等",
			Packages: []string{"tmux", "screen", "curl", "wget", "jq", "rsync", "tree", "ncdu", "pigz", "zip", "p7zip-full", "pv", "unzip", "bash-completion", "expect"}},
		{Name: "svc", Title: "网络服务", Description: "openssh-server/nfs/chrony/rsyslog 等",
			Packages: []string{"openssh-server", "rsyslog", "nfs-kernel-server", "nfs-common", "rpcbind", "squid", "isc-dhcp-client", "chrony", "lldpd", "tuned"}},
		{Name: "ofed-deps", Title: "OFED 编译依赖", Description: "quilt/tk/graphviz/libnl 等（IB 驱动构建）",
			Packages: []string{"quilt", "tk", "graphviz", "swig", "libfuse2t64", "chrpath", "libnl-route-3-dev", "libnl-3-dev"}},
	}
}

// GroupByName returns a group by its name.
func GroupByName(name string) (Group, bool) {
	for _, g := range DefaultGroups() {
		if g.Name == name {
			return g, true
		}
	}
	return Group{}, false
}

// Available reports whether apt tooling is present (Linux host with apt).
func Available() bool {
	_, e1 := exec.LookPath("apt-get")
	_, e2 := exec.LookPath("apt-cache")
	return e1 == nil && e2 == nil
}

// Downloader fetches package groups into a debs directory.
type Downloader struct {
	// DebsDir is template/debs; groups land in DebsDir/<group>/.
	DebsDir string
}

// resolveClosure returns the full recursive dependency closure (package names)
// for the given top-level packages, filtering out virtual packages and
// alternatives that apt-get download cannot fetch.
func resolveClosure(ctx context.Context, pkgs []string) ([]string, error) {
	args := append([]string{"depends", "--recurse", "--no-recommends", "--no-suggests",
		"--no-conflicts", "--no-breaks", "--no-replaces", "--no-enhances", "--no-pre-depends"}, pkgs...)
	cmd := exec.CommandContext(ctx, "apt-cache", args...)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("apt-cache depends: %w", err)
	}
	set := map[string]struct{}{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Package lines start at column 0 (no leading space); deps are indented.
		if line == "" || line[0] == ' ' {
			continue
		}
		name := strings.TrimSpace(line)
		// Skip virtual packages (apt marks them "<name>") and arch-qualified dups.
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

// Download resolves and downloads a group's closure into dir. When targetCodename
// differs from the build host's release (e.g. building a 24.04/noble ISO on a
// 26.04/resolute host), the resolve+download runs INSIDE an ubuntu:<ver>
// container so the debs are the target release's versions, not the host's.
// When they match (or the target is unknown), it uses the host apt directly.
// No index is written; the builder regenerates the offline index per workspace.
func (d *Downloader) Download(ctx context.Context, g Group, dir, targetCodename string, log io.Writer) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	host := HostCodename()
	// Container path: target known, differs from host, docker + image available.
	if targetCodename != "" && targetCodename != host && dockerAvailable() && imageTagForCodename(targetCodename) != "" {
		return d.downloadInContainer(ctx, g, dir, targetCodename, log)
	}

	// Host path: target matches host (or unknown) — resolve+download on the host.
	if !Available() {
		return fmt.Errorf("apt not available on this host (tool-package download runs on the Linux server)")
	}
	if targetCodename != "" && targetCodename != host {
		fmt.Fprintf(log, "warning: target %s != host %s but no container available; falling back to host apt (versions may not match)\n", targetCodename, host)
	}
	fmt.Fprintf(log, "resolving dependency closure for group %q (%d top-level packages)\n", g.Name, len(g.Packages))
	closure, err := resolveClosure(ctx, g.Packages)
	if err != nil {
		return err
	}
	fmt.Fprintf(log, "closure has %d packages; downloading into %s\n", len(closure), dir)

	// apt-get download must run in the target dir (it writes to CWD).
	cmd := exec.CommandContext(ctx, "apt-get", append([]string{"download"}, closure...)...)
	cmd.Dir = dir
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		// apt-get download exits non-zero if any single pkg fails; surface it.
		fmt.Fprintf(log, "warning: apt-get download reported errors (some packages may be unavailable): %v\n", err)
	}
	return nil
}

// GenerateIndex regenerates Packages/Packages.gz across the whole debs dir so it
// works as an offline apt source (deb [trusted=yes] file:///cdrom/debs ./).
func (d *Downloader) GenerateIndex(ctx context.Context, log io.Writer) error {
	fmt.Fprintln(log, "regenerating offline apt index (Packages/Packages.gz)")
	// dpkg-scanpackages -m . /dev/null > Packages ; gzip -9c > Packages.gz
	scan := exec.CommandContext(ctx, "dpkg-scanpackages", "-m", ".", "/dev/null")
	scan.Dir = d.DebsDir
	pkgsData, err := scan.Output()
	if err != nil {
		return fmt.Errorf("dpkg-scanpackages: %w", err)
	}
	if err := os.WriteFile(filepath.Join(d.DebsDir, "Packages"), pkgsData, 0o644); err != nil {
		return err
	}
	// gzip
	var gz bytes.Buffer
	gzcmd := exec.CommandContext(ctx, "gzip", "-9c")
	gzcmd.Stdin = bytes.NewReader(pkgsData)
	gzcmd.Stdout = &gz
	if err := gzcmd.Run(); err != nil {
		return fmt.Errorf("gzip Packages: %w", err)
	}
	if err := os.WriteFile(filepath.Join(d.DebsDir, "Packages.gz"), gz.Bytes(), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(log, "index written: %d bytes\n", len(pkgsData))
	return nil
}
