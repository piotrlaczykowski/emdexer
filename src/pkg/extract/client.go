package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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
type Client struct {
	CB   *extractor.CircuitBreaker
	FS   vfs.FileSystem
	HTTP *http.Client
}

// internalExts are file extensions handled directly without the Extractous sidecar.
var internalExts = map[string]bool{".txt": true, ".md": true, ".go": true, ".py": true, ".json": true}

// ExtractFromBytes extracts text content from raw bytes, using the Extractous sidecar
// for non-text formats.
func (c *Client) ExtractFromBytes(path string, data []byte, extractousHost string) (string, error) {
	ext := strings.ToLower(filepath.Ext(path))
	if internalExts[ext] {
		return string(data), nil
	}

	if !c.CB.Allow() {
		return "", fmt.Errorf("cb open")
	}

	bodyBuf := &bytes.Buffer{}
	writer := multipart.NewWriter(bodyBuf)
	part, _ := writer.CreateFormFile("file", filepath.Base(path))
	_, _ = part.Write(data)
	_ = writer.Close()
	req, _ := http.NewRequest("POST", extractousHost+"/extract", bodyBuf)
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
func (c *Client) ExtractContent(path, extractousHost string) (string, error) {
	f, err := c.FS.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	data, _ := io.ReadAll(f)
	return c.ExtractFromBytes(path, data, extractousHost)
}
