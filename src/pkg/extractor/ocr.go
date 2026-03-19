package extractor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"path/filepath"
)

// OCRAgent performs optical character recognition on image content by routing
// to the Extractous sidecar with the ocr=true parameter. Zero tokens burned.
//
// Supported formats: .png, .jpg, .jpeg, .tiff, .tif, .bmp
type OCRAgent struct {
	ExtractousHost string       // e.g. "http://extractous:8000"
	HTTP           *http.Client // reuse from caller; nil = http.DefaultClient
}

// ocrResult matches the Extractous sidecar JSON response.
type ocrResult struct {
	Text     string                 `json:"text"`
	Metadata map[string]interface{} `json:"metadata"`
}

// Extract performs OCR on raw image bytes by sending them to the Extractous
// sidecar with ?ocr=true. The filename hint helps the sidecar choose the
// correct decoder.
func (o *OCRAgent) Extract(ctx context.Context, filename string, content []byte) (string, error) {
	if o.ExtractousHost == "" {
		return "", fmt.Errorf("OCR: extractous host not configured")
	}

	client := o.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(filename))
	if err != nil {
		return "", fmt.Errorf("OCR: create form file: %w", err)
	}
	if _, err = part.Write(content); err != nil {
		return "", fmt.Errorf("OCR: write content: %w", err)
	}
	if err = writer.Close(); err != nil {
		return "", fmt.Errorf("OCR: close writer: %w", err)
	}

	endpoint := o.ExtractousHost + "/extract?ocr=true"
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, body)
	if err != nil {
		return "", fmt.Errorf("OCR: create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("OCR: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("OCR: extractous returned HTTP %d", resp.StatusCode)
	}

	var result ocrResult
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("OCR: decode response: %w", err)
	}

	return result.Text, nil
}
