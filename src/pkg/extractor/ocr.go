package extractor

import (
	"context"
	"fmt"
)

// OCRAgent performs optical character recognition on image content.
// Currently, direct OCR via Tesseract requires the binary to be present
// and the content piped via stdin is not reliably supported.
// Route image files through the Extractous sidecar instead.
type OCRAgent struct{}

// Extract attempts OCR on raw image bytes.
// NOT YET IMPLEMENTED — use the Extractous sidecar for image extraction.
// The Extractous sidecar handles PDF, DOCX, images and more via /extract endpoint.
func (o *OCRAgent) Extract(ctx context.Context, content []byte) (string, error) {
	return "", fmt.Errorf("OCR direct extraction not yet implemented: route image files through the Extractous sidecar (/extract endpoint)")
}
