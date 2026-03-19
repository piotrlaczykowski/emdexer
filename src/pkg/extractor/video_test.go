package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// zeroReader is an infinite source of zero bytes — simulates a large file
// without allocating the payload in memory. Wrap with io.LimitReader to bound it.
type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

// errAfterReader returns an error once exactly n bytes have been delivered.
// Setting remaining=0 causes failure on the very first Read call.
type errAfterReader struct {
	remaining int64
	err       error
}

func (r *errAfterReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, r.err
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	for i := int64(0); i < n; i++ {
		p[i] = 0
	}
	r.remaining -= n
	return int(n), nil
}

// parseMultipartRequest extracts all form parts from an incoming multipart request.
// Returns part names → content bytes, plus the filename of the "file" part.
func parseMultipartRequest(t *testing.T, r *http.Request) (parts map[string][]byte, filename string) {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("ParseMediaType: %v", err)
	}
	if !strings.HasPrefix(mediaType, "multipart/") {
		t.Fatalf("expected multipart Content-Type, got %q", mediaType)
	}

	parts = make(map[string][]byte)
	mr := multipart.NewReader(r.Body, params["boundary"])
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextPart: %v", err)
		}
		data, _ := io.ReadAll(part)
		parts[part.FormName()] = data
		if part.FormName() == "file" {
			filename = part.FileName()
		}
	}
	return parts, filename
}

// TestVideoSampler_ProcessWhisper is the baseline happy-path test (kept from original).
func TestVideoSampler_ProcessWhisper(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("Content-Type") == "" {
			t.Error("missing Content-Type header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperTranscription{Text: "Transcribed text from video"})
	}))
	defer ts.Close()

	v := &VideoSampler{
		WhisperURL:   ts.URL,
		WhisperModel: "base",
		HTTP:         ts.Client(),
	}

	text, err := v.ProcessWhisper(context.Background(), "test.mp4", io.LimitReader(zeroReader{}, 64))
	if err != nil {
		t.Fatalf("ProcessWhisper failed: %v", err)
	}
	if text != "Transcribed text from video" {
		t.Errorf("expected 'Transcribed text from video', got %q", text)
	}
}

func TestVideoSampler_SampleFrames(t *testing.T) {
	v := &VideoSampler{}
	_, err := v.SampleFrames(context.Background(), "test.mp4")
	if err == nil {
		t.Error("expected error for SampleFrames (not implemented)")
	}
}

// TestProcessWhisper_MultipartIntegrity parses the incoming multipart stream on the
// server side and verifies that every required field is present and correct.
func TestProcessWhisper_MultipartIntegrity(t *testing.T) {
	const payloadSize = 4096

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts, filename := parseMultipartRequest(t, r)

		// "file" part must exist with the original filename.
		fileData, ok := parts["file"]
		if !ok {
			t.Error("missing 'file' part in multipart body")
		} else if len(fileData) != payloadSize {
			t.Errorf("file part: want %d bytes, got %d", payloadSize, len(fileData))
		}
		if filename != "audio.mp3" {
			t.Errorf("file part filename: want %q, got %q", "audio.mp3", filename)
		}

		// "model" field must match the configured model.
		if model := string(parts["model"]); model != "small" {
			t.Errorf("model field: want %q, got %q", "small", model)
		}

		// "response_format" must be "json".
		if rf := string(parts["response_format"]); rf != "json" {
			t.Errorf("response_format field: want %q, got %q", "json", rf)
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperTranscription{Text: "ok"})
	}))
	defer ts.Close()

	v := &VideoSampler{
		WhisperURL:   ts.URL,
		WhisperModel: "small",
		HTTP:         ts.Client(),
	}

	// io.LimitReader over zeroReader: payloadSize bytes, zero heap allocation.
	text, err := v.ProcessWhisper(context.Background(), "audio.mp3", io.LimitReader(zeroReader{}, payloadSize))
	if err != nil {
		t.Fatalf("ProcessWhisper failed: %v", err)
	}
	if text != "ok" {
		t.Errorf("expected %q, got %q", "ok", text)
	}
}

// TestProcessWhisper_DefaultModel verifies that an empty WhisperModel falls back to "base".
func TestProcessWhisper_DefaultModel(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts, _ := parseMultipartRequest(t, r)
		if model := string(parts["model"]); model != "base" {
			t.Errorf("model field: want %q, got %q", "base", model)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperTranscription{Text: "ok"})
	}))
	defer ts.Close()

	v := &VideoSampler{
		WhisperURL:   ts.URL,
		WhisperModel: "", // deliberately empty — should default to "base"
		HTTP:         ts.Client(),
	}

	_, err := v.ProcessWhisper(context.Background(), "clip.wav", io.LimitReader(zeroReader{}, 64))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestProcessWhisper_LargeFileStreaming uses a 64 MB virtual reader that never
// allocates the payload. If ProcessWhisper buffered the whole file in memory
// (as the old bytes.Buffer implementation did) the goroutine would allocate
// 128 MB+ and the OS would OOM-kill this test under tight memory limits.
// Passing with a zeroReader proves the pipe-based streaming path is active.
func TestProcessWhisper_LargeFileStreaming(t *testing.T) {
	const fileSize = 64 << 20 // 64 MiB virtual file

	var receivedBytes int64

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			t.Errorf("bad Content-Type: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("NextPart: %v", err)
				return
			}
			n, copyErr := io.Copy(io.Discard, part)
			if part.FormName() == "file" {
				receivedBytes = n
			}
			if copyErr != nil {
				t.Errorf("copy part %q: %v", part.FormName(), copyErr)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(whisperTranscription{Text: "streamed"})
	}))
	defer ts.Close()

	v := &VideoSampler{
		WhisperURL:   ts.URL,
		WhisperModel: "base",
		HTTP:         ts.Client(),
	}

	text, err := v.ProcessWhisper(context.Background(), "large.wav", io.LimitReader(zeroReader{}, fileSize))
	if err != nil {
		t.Fatalf("ProcessWhisper failed: %v", err)
	}
	if text != "streamed" {
		t.Errorf("expected %q, got %q", "streamed", text)
	}
	if receivedBytes != fileSize {
		t.Errorf("server received %d bytes in file part, want %d", receivedBytes, fileSize)
	}
}

// TestProcessWhisper_NotConfigured checks the early-return when WhisperURL is empty.
func TestProcessWhisper_NotConfigured(t *testing.T) {
	v := &VideoSampler{}
	_, err := v.ProcessWhisper(context.Background(), "test.mp3", io.LimitReader(zeroReader{}, 64))
	if err == nil {
		t.Error("expected error when WhisperURL is empty")
	}
}

// TestProcessWhisper_HTTPError verifies error propagation for non-200 responses.
func TestProcessWhisper_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain body to avoid broken-pipe on the client side.
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("sidecar overloaded"))
	}))
	defer ts.Close()

	v := &VideoSampler{
		WhisperURL: ts.URL,
		HTTP:       ts.Client(),
	}

	_, err := v.ProcessWhisper(context.Background(), "test.mp3", io.LimitReader(zeroReader{}, 64))
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected '500' in error, got: %v", err)
	}
}

// TestProcessWhisper_MidStreamError verifies that a source reader failure propagates
// back through the io.Pipe and surfaces as an error from ProcessWhisper.
//
// errAfterReader(remaining=0) fails on the first Read call, which occurs inside
// the multipart-writing goroutine after the boundary header has been written.
// The goroutine calls pw.CloseWithError(wrappedErr), which causes the next read
// from the pipe reader (held by the HTTP transport) to return that error,
// aborting the request and causing client.Do to return an error.
func TestProcessWhisper_MidStreamError(t *testing.T) {
	sentinelErr := errors.New("simulated disk read error")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain whatever bytes arrived; connection may be reset before completion.
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	v := &VideoSampler{
		WhisperURL: ts.URL,
		HTTP:       ts.Client(),
	}

	r := &errAfterReader{remaining: 0, err: sentinelErr}
	_, err := v.ProcessWhisper(context.Background(), "bad.mp3", r)
	if err == nil {
		t.Fatal("expected error when source reader fails")
	}
	if !errors.Is(err, sentinelErr) {
		t.Errorf("expected error chain to contain sentinel error\ngot: %v", err)
	}
}
