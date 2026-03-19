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
// It routes files to the appropriate sidecar based on extension:
//   - Text files → returned as-is (no sidecar)
//   - Images (.png, .jpg, .jpeg, .tiff) → Extractous with ocr=true
//   - Audio/Video (.mp3, .wav, .mp4, .mkv, etc.) → Whisper sidecar
//   - PDFs → Extractous, with OCR fallback if near-zero text extracted
//   - Everything else → Extractous sidecar
type Client struct {
	CB        *extractor.CircuitBreaker
	FS        vfs.FileSystem
	HTTP      *http.Client
	Whisper   *WhisperClient // nil if Whisper sidecar is not configured
	EnableOCR bool           // when true, images are sent to Extractous with ocr=true
}

// internalExts are file extensions handled directly without the Extractous sidecar.
var internalExts = map[string]bool{".txt": true, ".md": true, ".go": true, ".py": true, ".json": true}

// imageExts are file extensions routed to Extractous with ocr=true for OCR processing.
var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".tiff": true, ".tif": true, ".bmp": true}

// pdfOCRMinChars is the minimum number of characters from a PDF extraction
// before triggering an OCR retry. Scanned PDFs often return near-zero text.
const pdfOCRMinChars = 50

// ExtractFromBytes extracts text content from raw bytes, routing to the
// appropriate sidecar based on file extension.
// For large audio/video files prefer ExtractContent which streams without buffering.
func (c *Client) ExtractFromBytes(path string, data []byte, extractousHost string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))

	// 1. Plain text — return as-is.
	if internalExts[ext] {
		return string(data), nil
	}

	// 2. Audio/Video — route to Whisper sidecar.
	//    bytes.NewReader wraps without copying, so memory = 1× file size (not 2×).
	if IsAudioExt(path) {
		if c.Whisper == nil {
			return "", fmt.Errorf("whisper sidecar not configured for %s", ext)
		}
		return c.Whisper.Transcribe(context.Background(), path, bytes.NewReader(data))
	}

	// 3. Images — route to Extractous with OCR flag.
	if imageExts[ext] {
		if !c.EnableOCR {
			return "", fmt.Errorf("OCR disabled: set EMDEX_ENABLE_OCR=true to extract text from images")
		}
		return c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, true)
	}

	// 4. PDF — extract normally, then OCR fallback if near-zero text.
	if ext == ".pdf" {
		text, err := c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, false)
		if err != nil {
			return "", err
		}
		if c.EnableOCR && len(strings.TrimSpace(text)) < pdfOCRMinChars {
			log.Printf("[extract] PDF %s returned %d chars — retrying with OCR", path, len(strings.TrimSpace(text)))
			ocrText, ocrErr := c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, true)
			if ocrErr == nil && len(strings.TrimSpace(ocrText)) > len(strings.TrimSpace(text)) {
				return ocrText, nil
			}
		}
		return text, nil
	}

	// 5. Everything else — Extractous sidecar (DOCX, XLSX, etc.)
	return c.extractViaExtractous(path, bytes.NewReader(data), extractousHost, false)
}

// extractViaExtractous sends file content from r to the Extractous sidecar. When ocr is
// true, the ?ocr=true query parameter is appended to enable Tesseract-based OCR.
// r is streamed via io.Pipe so only a small pipe buffer is held in memory.
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
		_ = pr.CloseWithError(err) // unblock the writer goroutine
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
// For audio/video files the VFS stream is piped directly to the Whisper sidecar
// without buffering the full file in memory, supporting files >500 MB.
func (c *Client) ExtractContent(path, extractousHost string) (string, error) {
	// Audio/video: stream directly to Whisper — never call io.ReadAll on large media.
	if IsAudioExt(path) {
		if c.Whisper == nil {
			return "", fmt.Errorf("whisper sidecar not configured for %s", filepath.Ext(path))
		}
		f, err := c.FS.Open(path)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		return c.Whisper.Transcribe(context.Background(), path, f)
	}

	// All other types: buffer once, then route through ExtractFromBytes.
	f, err := c.FS.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	return c.ExtractFromBytes(path, data, extractousHost)
}
