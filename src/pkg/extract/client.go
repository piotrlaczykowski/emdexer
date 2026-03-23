package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"github.com/piotrlaczykowski/emdexer/extractor"
	"github.com/piotrlaczykowski/emdexer/vfs"
)

// Result represents the response from the Extractous sidecar.
type Result struct {
	Text     string                 `json:"text"`
	Metadata map[string]interface{} `json:"metadata"`
}

// Client wraps content extraction with circuit breaker and VFS support.
// It routes files to the appropriate sidecar based on extension and configuration:
//
//   - Text files (.txt/.md/.go/.py/.json) → returned as-is
//   - Images (.png/.jpg/.jpeg/.tiff/.bmp):
//     → EMDEX_VISION_ENABLED=true  → Gemini Vision caption (takes priority)
//     → EMDEX_ENABLE_OCR=true      → Extractous + Tesseract OCR
//     → neither                    → error (file skipped)
//   - Video (.mp4/.mkv/.avi/.mov/.webm):
//     → Whisper (audio track) and/or FFmpeg frames → captioned via Vision
//     → neither enabled            → skipped
//   - Audio (.mp3/.wav/.m4a/.ogg/.flac) → Whisper sidecar
//   - PDFs → Extractous, with OCR fallback if near-zero text
//   - Everything else → Extractous sidecar
type Client struct {
	CB        *extractor.CircuitBreaker
	FS        vfs.FileSystem
	HTTP      *http.Client
	Whisper   *WhisperClient // nil if Whisper sidecar is not configured
	Frames    *FFmpegClient  // nil if FFmpeg sidecar is not configured
	EnableOCR bool           // when true, images are sent to Extractous with ocr=true

	// Vision configuration
	VisionEnabled   bool   // when true, images are captioned by Gemini Vision
	VisionMaxSizeMB int    // skip images larger than this (0 = no limit)
	VisionAPIKey    string // Google API key used for Gemini Vision calls
}

// internalExts are file extensions handled directly without the Extractous sidecar.
var internalExts = map[string]bool{".txt": true, ".md": true, ".go": true, ".py": true, ".json": true}

// imageExts are file extensions routed to Extractous with ocr=true for OCR processing.
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".tiff": true, ".tif": true, ".bmp": true}

// pdfOCRMinChars is the minimum number of characters from a PDF extraction
// before triggering an OCR retry. Scanned PDFs often return near-zero text.
const pdfOCRMinChars = 50

// ExtractFromBytes extracts text content from raw bytes, routing to the
// appropriate sidecar based on file extension and client configuration.
// The second return value carries optional extra metadata (e.g. whisper segments,
// extraction_method) to be stored in the Qdrant point payload by the pipeline.
func (c *Client) ExtractFromBytes(path string, data []byte, extractousHost string) (string, map[string]string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	ctx := context.Background()

	// 1. Plain text — return as-is.
	if internalExts[ext] {
		return string(data), nil, nil
	}

	// 2. Video — route to Whisper (audio track) and/or FFmpeg frames.
	if IsVideoExt(path) {
		return c.extractVideo(ctx, path, data)
	}

	// 3. Pure audio — route to Whisper sidecar only.
	if IsAudioExt(path) {
		return c.extractAudio(ctx, path, data)
	}

	// 4. Images — Vision takes priority over OCR.
	if imageExts[ext] {
		return c.extractImage(ctx, path, data, extractousHost)
	}

	// 5. PDF — extract normally, then OCR fallback if near-zero text.
	if ext == ".pdf" {
		text, err := c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, false)
		if err != nil {
			return "", nil, err
		}
		if c.EnableOCR && len(strings.TrimSpace(text)) < pdfOCRMinChars {
			log.Printf("[extract] PDF %s returned %d chars — retrying with OCR", path, len(strings.TrimSpace(text)))
			ocrText, ocrErr := c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, true)
			if ocrErr == nil && len(strings.TrimSpace(ocrText)) > len(strings.TrimSpace(text)) {
				return ocrText, nil, nil
			}
		}
		return text, nil, nil
	}

	// 6. Everything else — Extractous sidecar (DOCX, XLSX, etc.)
	text, err := c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, false)
	return text, nil, err
}

// extractVideo handles video files by combining Whisper audio transcription
// and/or FFmpeg frame extraction (with optional Vision captioning).
func (c *Client) extractVideo(ctx context.Context, path string, data []byte) (string, map[string]string, error) {
	var parts []string
	var extraMeta map[string]string

	// Audio track via Whisper
	if c.Whisper != nil && c.Whisper.Enabled {
		txt, segs, err := c.Whisper.Transcribe(ctx, path, data)
		if err != nil {
			log.Printf("[extract] WARN: Whisper failed for %s: %v", path, err)
		} else if txt != "" {
			parts = append(parts, txt)
			if len(segs) > 0 {
				if segsJSON, merr := json.Marshal(segs); merr == nil {
					if extraMeta == nil {
						extraMeta = make(map[string]string)
					}
					extraMeta["segments"] = string(segsJSON)
				}
			}
		}
	}

	// Video frames via FFmpeg sidecar
	if c.Frames != nil {
		frames, err := c.Frames.ExtractFrames(ctx, data, path)
		if err != nil {
			log.Printf("[extract] WARN: FFmpeg frame extraction failed for %s: %v", path, err)
		} else if len(frames) > 0 {
			if c.VisionEnabled {
				for _, frame := range frames {
					caption, cerr := captionWithMetrics(ctx, c.VisionAPIKey, frame.Data, "image/jpeg")
					if cerr == nil && caption != "" {
						parts = append(parts, fmt.Sprintf("[Frame at %ds] %s", frame.TimestampSec, caption))
					}
				}
			}
		}
	}

	if len(parts) == 0 {
		log.Printf("[extract] INFO: Skipping video %s — no audio/video extractor enabled or all failed", path)
		videoSkippedTotal.Inc()
		return "", nil, nil
	}
	return strings.Join(parts, "\n\n"), extraMeta, nil
}

// extractAudio handles pure audio files (mp3, wav, m4a, ogg, flac) via Whisper.
func (c *Client) extractAudio(ctx context.Context, path string, data []byte) (string, map[string]string, error) {
	if c.Whisper == nil {
		return "", nil, fmt.Errorf("whisper sidecar not configured for %s", filepath.Ext(path))
	}
	if !c.Whisper.Enabled {
		log.Printf("[extract] INFO: Skipping audio %s — EMDEX_WHISPER_ENABLED=false", path)
		whisperAudioSkippedTotal.Inc()
		return "", nil, nil
	}
	txt, segs, err := c.Whisper.Transcribe(ctx, path, data)
	if err != nil {
		return "", nil, err
	}
	var extraMeta map[string]string
	if len(segs) > 0 {
		if segsJSON, merr := json.Marshal(segs); merr == nil {
			extraMeta = map[string]string{"segments": string(segsJSON)}
		}
	}
	return txt, extraMeta, nil
}

// extractImage handles image files with Vision or OCR routing.
func (c *Client) extractImage(ctx context.Context, path string, data []byte, extractousHost string) (string, map[string]string, error) {
	ext := strings.ToLower(filepath.Ext(path))

	// Vision takes priority.
	if c.VisionEnabled {
		if c.VisionMaxSizeMB > 0 && len(data) > c.VisionMaxSizeMB*1024*1024 {
			log.Printf("[extract] INFO: Skipping %s — %.1f MB exceeds vision limit (%d MB)",
				path, float64(len(data))/(1024*1024), c.VisionMaxSizeMB)
			visionCallsTotal.WithLabelValues("skipped_size").Inc()
			return "", nil, nil
		}
		caption, err := captionWithMetrics(ctx, c.VisionAPIKey, data, imageMimeType(ext))
		if err != nil {
			log.Printf("[extract] WARN: Vision captioning failed for %s: %v", path, err)
			return "", nil, nil
		}
		return caption, map[string]string{"extraction_method": "gemini-vision"}, nil
	}

	// Fall back to OCR via Extractous.
	if !c.EnableOCR {
		return "", nil, fmt.Errorf("OCR disabled: set EMDEX_ENABLE_OCR=true to extract text from images")
	}
	text, err := c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, true)
	return text, nil, err
}

// extractViaExtractous sends file content from r to the Extractous sidecar. When ocr is
// true, the ?ocr=true query parameter is appended to enable Tesseract-based OCR.
func (c *Client) extractViaExtractous(path string, r io.Reader, extractousHost string, ocr bool) (string, error) {
	if !c.CB.Allow() {
		return "", fmt.Errorf("cb open")
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)

	go func() {
		var werr error
		defer func() { pw.CloseWithError(werr) }()

		part, err := writer.CreateFormFile("file", filepath.Base(path))
		if err != nil {
			werr = err
			return
		}
		if _, err = io.Copy(part, r); err != nil {
			werr = err
			return
		}
		werr = writer.Close()
	}()

	endpoint := extractousHost + "/extract"
	if ocr {
		endpoint += "?ocr=true"
	}

	req, err := http.NewRequest("POST", endpoint, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	res, err := c.HTTP.Do(req)
	if err != nil {
		c.CB.RecordFailure()
		return "", err
	}
	defer func() { _ = res.Body.Close() }()

	if res.StatusCode != http.StatusOK {
		c.CB.RecordFailure()
		return "", fmt.Errorf("extraction API %d", res.StatusCode)
	}

	c.CB.RecordSuccess()
	var result Result
	_ = json.NewDecoder(res.Body).Decode(&result)
	return result.Text, nil
}

// ExtractContent reads a file from the VFS and extracts its text content.
// The second return value carries optional extra metadata for the Qdrant payload.
func (c *Client) ExtractContent(path, extractousHost string) (string, map[string]string, error) {
	// Audio/video: read into memory then route through ExtractFromBytes.
	if IsAudioExt(path) {
		f, err := c.FS.Open(path)
		if err != nil {
			return "", nil, err
		}
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(f)
		if err != nil {
			return "", nil, err
		}
		return c.ExtractFromBytes(path, data, extractousHost)
	}

	// All other types: buffer once, then route through ExtractFromBytes.
	f, err := c.FS.Open(path)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", nil, err
	}
	return c.ExtractFromBytes(path, data, extractousHost)
}

