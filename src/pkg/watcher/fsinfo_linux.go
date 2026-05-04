//go:build linux

package watcher

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

var ephemeralTypes = map[string]struct{}{
	"tmpfs":   {},
	"ramfs":   {},
	"overlay": {},
}

func isEphemeralFS(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer f.Close()

	bestPrefix := ""
	bestType := ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 9 {
			continue
		}
		mp := fields[4]
		sep := -1
		for i := 6; i < len(fields); i++ {
			if fields[i] == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+1 >= len(fields) {
			continue
		}
		fsType := fields[sep+1]
		if !strings.HasPrefix(abs, mp) {
			continue
		}
		if len(mp) > len(bestPrefix) {
			bestPrefix = mp
			bestType = fsType
		}
	}
	_, eph := ephemeralTypes[bestType]
	return eph
}
