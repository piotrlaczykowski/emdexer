package indexer

import (
	"testing"
)

func TestBuildWalkConfig_AllDisabled(t *testing.T) {
	cfg := BuildWalkConfig(false, false, false, false)

	for _, ext := range []string{".jpg", ".mp4", ".mp3", ".dng"} {
		if !cfg.SkipExts[ext] {
			t.Errorf("expected %s to be skipped when all features disabled", ext)
		}
	}
}

func TestBuildWalkConfig_OCREnabled_KeepsImages(t *testing.T) {
	cfg := BuildWalkConfig(false, false, false, true)

	// OCR can process images — must NOT skip them
	if cfg.SkipExts[".jpg"] {
		t.Error("expected .jpg NOT in SkipExts when OCR is enabled")
	}
	// Audio still skipped (whisper off)
	if !cfg.SkipExts[".mp3"] {
		t.Error("expected .mp3 in SkipExts when whisper is disabled")
	}
}

func TestBuildWalkConfig_WhisperEnabled_KeepsAudio(t *testing.T) {
	cfg := BuildWalkConfig(true, false, false, false)

	// Whisper handles audio and video audio tracks — must NOT skip
	if cfg.SkipExts[".mp3"] {
		t.Error("expected .mp3 NOT in SkipExts when whisper is enabled")
	}
	if cfg.SkipExts[".mp4"] {
		t.Error("expected .mp4 NOT in SkipExts when whisper is enabled (audio track)")
	}
	// Vision and OCR both off — images must be skipped
	if !cfg.SkipExts[".jpg"] {
		t.Error("expected .jpg in SkipExts when vision and OCR are both disabled")
	}
}

func TestBuildWalkConfig_RawAlwaysSkipped(t *testing.T) {
	for _, flags := range [][4]bool{
		{false, false, false, false},
		{true, true, true, true},
		{true, false, true, false},
	} {
		cfg := BuildWalkConfig(flags[0], flags[1], flags[2], flags[3])
		for _, ext := range []string{".dng", ".cr2", ".nef", ".arw"} {
			if !cfg.SkipExts[ext] {
				t.Errorf("expected %s always skipped (flags=%v)", ext, flags)
			}
		}
	}
}

func TestWalkConfig_ShouldExclude(t *testing.T) {
	cfg := WalkConfig{
		ExcludePaths: []string{"#recycle", "*.tmp"},
	}

	cases := []struct {
		name     string
		fullPath string
		want     bool
	}{
		{"#recycle", "/mnt/nas/#recycle", true},
		{"file.tmp", "/docs/file.tmp", true},
		{"important.go", "/src/important.go", false},
		{"normal.txt", "/docs/normal.txt", false},
	}

	for _, tc := range cases {
		got := cfg.shouldExclude(tc.name, tc.fullPath)
		if got != tc.want {
			t.Errorf("shouldExclude(%q, %q) = %v, want %v", tc.name, tc.fullPath, got, tc.want)
		}
	}
}
