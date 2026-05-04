package watcher

import (
	"runtime"
	"testing"
)

func TestIsEphemeralFS_RegularDirNotEphemeral(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ephemeral FS detection is Linux-only")
	}
	if got := IsEphemeralFS(t.TempDir()); got {
		t.Fatalf("temp dir should not be tmpfs in CI; got ephemeral=true")
	}
}

func TestIsEphemeralFS_DarwinReturnsFalse(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-only")
	}
	if got := IsEphemeralFS("/tmp"); got {
		t.Fatalf("darwin should always return false; got true")
	}
}
