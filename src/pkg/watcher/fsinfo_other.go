//go:build !linux

package watcher

func isEphemeralFS(string) bool { return false }
