package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/piotrlaczykowski/emdexer/extractor"
)

type ExtractedResult struct {
	Text     string                 `json:"text"`
	Metadata map[string]interface{} `json:"metadata"`
}

var globalCB *extractor.CircuitBreaker

func extractFromBytes(path string, data []byte, extractousHost string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	internalExts := map[string]bool{".txt": true, ".md": true, ".go": true, ".py": true, ".json": true}
	if internalExts[ext] {
		return string(data), nil
	}

	if !globalCB.Allow() {
		return "", fmt.Errorf("cb open")
	}

	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)
	part, err := writer.CreateFormFile("file", filepath.Base(path))
	if err != nil {
		globalCB.RecordFailure()
		return "", fmt.Errorf("failed to create multipart form: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		globalCB.RecordFailure()
		return "", fmt.Errorf("failed to write to multipart form: %w", err)
	}
	writer.Close()

	endpoint := strings.TrimSuffix(extractousHost, "/")
	if !strings.HasSuffix(endpoint, "/extract") {
		endpoint += "/extract"
	}

	req, err := http.NewRequest("POST", endpoint, bodyBuf)
	if err != nil {
		globalCB.RecordFailure()
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	client := &http.Client{Timeout: 60 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		globalCB.RecordFailure()
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		globalCB.RecordFailure()
		return "", fmt.Errorf("extraction API %d", res.StatusCode)
	}

	var result ExtractedResult
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil {
		globalCB.RecordFailure()
		return "", fmt.Errorf("failed to decode extraction response: %w", err)
	}

	globalCB.RecordSuccess()
	return result.Text, nil
}

func extractContent(path, extractousHost string) (string, error) {
	f, err := globalFS.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("read error: %w", err)
	}
	return extractFromBytes(path, data, extractousHost)
}
