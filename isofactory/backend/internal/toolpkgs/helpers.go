package toolpkgs

import (
	"errors"
	"fmt"
	"path/filepath"
)

var errBusy = errors.New("另一个工具包下载正在进行中，请稍候")

func errUnknownGroup(name string) error {
	return fmt.Errorf("未知的工具包分组: %s", name)
}

// countDebs counts .deb files in debsDir/<codename>/<group>/.
func countDebs(debsDir, codename, group string) int {
	matches, _ := filepath.Glob(filepath.Join(debsDir, codename, group, "*.deb"))
	return len(matches)
}
