package watcher

// IsEphemeralFS reports whether path lives on a filesystem that does NOT
// survive container/pod restarts (tmpfs, overlay, ramfs). Linux only; other
// platforms always return false.
func IsEphemeralFS(path string) bool {
	return isEphemeralFS(path)
}
