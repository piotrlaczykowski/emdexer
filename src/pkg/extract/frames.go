package extract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus metrics for the FFmpeg frame extraction path.
var (
	framesExtractedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_node_frames_extracted_total",
		Help: "Total number of video frames extracted by the FFmpeg sidecar",
	})
	framesDurationMs = promauto.NewHistogram(prometheus.HistogramOpts{
		Name:    "emdexer_node_frames_duration_ms",
		Help:    "Latency of FFmpeg frame extraction calls in milliseconds",
		Buckets: []float64{500, 1000, 2000, 5000, 10000, 30000},
	})
	videoSkippedTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "emdexer_node_video_skipped_total",
		Help: "Total number of video files skipped because no audio/video extractor was enabled",
	})
)

// Frame holds a single extracted video frame as a JPEG image.
type Frame struct {
	TimestampSec int    // position in the video
	Data         []byte // JPEG bytes
}

// ffmpegFrameEntry is one entry in the FFmpeg sidecar JSON response.
type ffmpegFrameEntry struct {
	TimestampSec int    `json:"timestamp_sec"`
	Data         string `json:"data"` // base64-encoded JPEG
}

// ffmpegFrameResponse is the full JSON response from the FFmpeg sidecar.
type ffmpegFrameResponse struct {
	Frames []ffmpegFrameEntry `json:"frames"`
}

// FFmpegClient calls the FFmpeg sidecar for video frame extraction.
type FFmpegClient struct {
	URL         string       // e.g. "http://ffmpeg-sidecar:8004"
	HTTP        *http.Client
	IntervalSec int // seconds between extracted frames (EMDEX_FRAME_INTERVAL_SEC)
	MaxFrames   int // maximum number of frames to return (EMDEX_MAX_FRAMES)
}

// ExtractFrames submits the video to the FFmpeg sidecar and returns up to MaxFrames JPEG frames.
func (f *FFmpegClient) ExtractFrames(ctx context.Context, data []byte, filename string) ([]Frame, error) {
	if f.URL == "" {
		return nil, fmt.Errorf("ffmpeg sidecar not configured (EMDEX_FFMPEG_URL is empty)")
	}

	interval := f.IntervalSec
	if interval <= 0 {
		interval = 30
	}
	maxFrames := f.MaxFrames
	if maxFrames <= 0 {
		maxFrames = 10
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: create form file: %w", err)
	}
	if _, err = io.Copy(part, bytes.NewReader(data)); err != nil {
		return nil, fmt.Errorf("ffmpeg: buffer content: %w", err)
	}
	if err = writer.Close(); err != nil {
		return nil, fmt.Errorf("ffmpeg: close writer: %w", err)
	}

	url := fmt.Sprintf("%s/frames?interval=%d&max_frames=%d", f.URL, interval, maxFrames)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := f.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ffmpeg: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ffmpeg: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var result ffmpegFrameResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("ffmpeg: decode response: %w", err)
	}

	// Cap at MaxFrames in case the sidecar returned more.
	entries := result.Frames
	if len(entries) > maxFrames {
		entries = entries[:maxFrames]
	}

	frames := make([]Frame, 0, len(entries))
	for _, e := range entries {
		jpegData, err := base64.StdEncoding.DecodeString(e.Data)
		if err != nil {
			continue // skip malformed frame
		}
		frames = append(frames, Frame{
			TimestampSec: e.TimestampSec,
			Data:         jpegData,
		})
	}

	framesExtractedTotal.Add(float64(len(frames)))
	return frames, nil
}
