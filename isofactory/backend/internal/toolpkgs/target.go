package toolpkgs

import (
	"bufio"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Ubuntu version → apt suite codename. Tool-package dependency closures must be
// resolved against the TARGET image's release, not the build host's, or the
// debs won't match (e.g. a 26.04 build-essential dropped into a 24.04 ISO).
var codenameByMajor = map[string]string{
	"22.04": "jammy",
	"24.04": "noble",
	"25.10": "questing",
	"26.04": "resolute",
}

var reMajorMinor = regexp.MustCompile(`^(\d+\.\d+)`)

// CodenameFor maps a system version like "24.04.4" (or "24.04") to its Ubuntu
// codename ("noble"). Returns "" if unknown.
func CodenameFor(systemVersion string) string {
	m := reMajorMinor.FindStringSubmatch(systemVersion)
	if m == nil {
		return ""
	}
	return codenameByMajor[m[1]]
}

// imageTagForCodename returns the docker image tag (ubuntu:<ver>) that provides
// the given codename's apt repository, or "" if we don't map it.
func imageTagForCodename(codename string) string {
	for major, cn := range codenameByMajor {
		if cn == codename {
			return "ubuntu:" + major
		}
	}
	return ""
}

// HostCodename reads /etc/os-release for the build host's own Ubuntu codename,
// so we can decide whether a target build needs a container (target != host).
func HostCodename() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "VERSION_CODENAME="); ok {
			return strings.Trim(v, `"`)
		}
	}
	return ""
}

// dockerAvailable reports whether a usable docker CLI is present.
func dockerAvailable() bool {
	if _, err := exec.LookPath("docker"); err != nil {
		return false
	}
	return true
}
