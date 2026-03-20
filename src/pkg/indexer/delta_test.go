package indexer

import (
	"bytes"
	"testing"
)

// readerAt adapts a []byte to io.ReaderAt for testing.
type bytesReaderAt struct{ b []byte }

func (r *bytesReaderAt) ReadAt(p []byte, off int64) (int, error) {
	return bytes.NewReader(r.b).ReadAt(p, off)
}

func TestCalculatePartialHash_SmallFile(t *testing.T) {
	content := []byte("hello, world")
	r := &bytesReaderAt{content}
	h1, err := CalculatePartialHash(r, int64(len(content)))
	if err != nil {
		t.Fatalf("CalculatePartialHash: %v", err)
	}
	if h1 == "" {
		t.Fatal("expected non-empty hash")
	}

	// Same content → same hash.
	h2, _ := CalculatePartialHash(&bytesReaderAt{content}, int64(len(content)))
	if h1 != h2 {
		t.Errorf("identical content should produce identical partial hash: %s != %s", h1, h2)
	}
}

func TestCalculatePartialHash_DifferentContent(t *testing.T) {
	a := []byte("content version 1")
	b := []byte("content version 2")
	ha, _ := CalculatePartialHash(&bytesReaderAt{a}, int64(len(a)))
	hb, _ := CalculatePartialHash(&bytesReaderAt{b}, int64(len(b)))
	if ha == hb {
		t.Error("different content should not produce the same partial hash")
	}
}

func TestCalculateFullHash_Deterministic(t *testing.T) {
	content := []byte("full hash test content 🔒")
	h1, err := CalculateFullHash(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("CalculateFullHash: %v", err)
	}
	h2, _ := CalculateFullHash(bytes.NewReader(content))
	if h1 != h2 {
		t.Errorf("full hash should be deterministic: %s != %s", h1, h2)
	}
}

func TestDeltaResultString(t *testing.T) {
	cases := []struct {
		r    DeltaResult
		want string
	}{
		{DeltaChanged, "Changed"},
		{DeltaStatChanged, "StatChanged"},
		{DeltaUnchanged, "Unchanged"},
	}
	for _, tc := range cases {
		if got := tc.r.String(); got != tc.want {
			t.Errorf("DeltaResult(%d).String() = %q; want %q", tc.r, got, tc.want)
		}
	}
}
