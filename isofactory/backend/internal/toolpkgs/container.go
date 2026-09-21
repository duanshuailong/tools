package toolpkgs

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// containerScript resolves the dependency closure and downloads every deb for a
// group INSIDE an ubuntu:<ver> container matching the target codename, so the
// packages (and their transitive deps) are the target release's versions rather
// than the build host's. Output lands in /out (bind-mounted to the cache dir)
// and is chowned back to the host user afterward.
//
// The apt mirror uses http:// (not https): the base ubuntu:<ver> image ships no
// ca-certificates, and deb integrity is guaranteed by the repo GPG signature
// regardless of transport — the traditional apt trust model.
//
// The closure is resolved the same way as the host path (apt-cache depends,
// keep top-level lines, drop virtual "<...>" packages and arch suffixes) but
// against the container's apt index. Downloaded files are owned by root inside
// the container, so we chown to the caller's uid:gid at the end.
const containerScriptTmpl = `set -e
export DEBIAN_FRONTEND=noninteractive
cat > /etc/apt/sources.list.d/ubuntu.sources <<SRCEOF
Types: deb
URIs: http://mirrors.aliyun.com/ubuntu/
Suites: %[1]s %[1]s-updates %[1]s-backports %[1]s-security
Components: main restricted universe multiverse
Signed-By: /usr/share/keyrings/ubuntu-archive-keyring.gpg
SRCEOF
rm -f /etc/apt/sources.list
apt-get update -qq
echo "resolving dependency closure in ubuntu:%[2]s container (%[3]d top-level packages)"
CLOSURE=$(apt-cache depends --recurse --no-recommends --no-suggests \
  --no-conflicts --no-breaks --no-replaces --no-enhances --no-pre-depends %[4]s \
  | grep -v '^ ' | grep -v '^<' | sed 's/:.*//' | sort -u)
N=$(echo "$CLOSURE" | grep -c .)
echo "closure has $N packages; downloading into /out"
cd /out
apt-get download $CLOSURE || echo "warning: apt-get download reported errors (some packages may be unavailable)"
chown -R %[5]d:%[6]d /out
`

// downloadInContainer runs the closure resolve+download for group g inside a
// container for targetCodename, writing debs into dir.
func (d *Downloader) downloadInContainer(ctx context.Context, g Group, dir, targetCodename string, log io.Writer) error {
	image := imageTagForCodename(targetCodename)
	if image == "" {
		return fmt.Errorf("no container image mapped for codename %q", targetCodename)
	}
	major := strings.TrimPrefix(image, "ubuntu:")

	script := fmt.Sprintf(containerScriptTmpl,
		targetCodename,          // %[1]s suite
		major,                   // %[2]s version label (log only)
		len(g.Packages),         // %[3]d top-level count
		strings.Join(g.Packages, " "), // %[4]s package specs
		os.Getuid(),             // %[5]d chown uid
		os.Getgid(),             // %[6]d chown gid
	)

	fmt.Fprintf(log, "using ubuntu:%s container for target %s (host differs)\n", major, targetCodename)
	args := []string{
		"run", "--rm",
		"-v", dir + ":/out",
		image,
		"bash", "-c", script,
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container tool-package download (ubuntu:%s): %w", major, err)
	}
	return nil
}
