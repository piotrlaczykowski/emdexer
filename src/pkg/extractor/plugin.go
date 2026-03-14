package extractor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type Result struct {
	Text     string                 `json:"text"`
	Metadata map[string]interface{} `json:"metadata"`
	Error    string                 `json:"error,omitempty"`
}

type Extractor interface {
	Extract(ctx context.Context, path string, mimeType string, content []byte) (*Result, error)
}

type PluginManager struct {
	PluginDir string
}

func NewPluginManager(dir string) *PluginManager {
	if dir == "" {
		dir = "/plugins"
	}
	return &PluginManager{PluginDir: dir}
}

func (pm *PluginManager) Discover() ([]string, error) {
	if _, err := os.Stat(pm.PluginDir); os.IsNotExist(err) {
		return nil, nil
	}

	var plugins []string
	entries, err := os.ReadDir(pm.PluginDir)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			path := filepath.Join(pm.PluginDir, entry.Name())
			// Check if executable
			info, err := entry.Info()
			if err == nil && info.Mode()&0111 != 0 {
				plugins = append(plugins, path)
			}
		}
	}
	return plugins, nil
}

func (pm *PluginManager) Extract(ctx context.Context, pluginPath string, path string, mimeType string, content []byte) (*Result, error) {
	// We'll pass the data via a temporary file or stdin
	// The requirement: {filepath, mime_type, bytes} -> Plugin returns {text, metadata}
	
	input := struct {
		FilePath string `json:"filepath"`
		MimeType string `json:"mime_type"`
		Bytes    []byte `json:"bytes"`
	}{
		FilePath: path,
		MimeType: mimeType,
		Bytes:    content,
	}

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, pluginPath)
	cmd.Stdin = os.Stdin // We'll pipe our own stdin if needed, but better use a dedicated buffer
	
	// Create a pipe for stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}

	go func() {
		defer stdin.Close()
		stdin.Write(inputJSON)
	}()

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("plugin failed: %s (stderr: %s)", err, string(exitErr.Stderr))
		}
		return nil, err
	}

	var result Result
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse plugin output: %w", err)
	}

	if result.Error != "" {
		return nil, fmt.Errorf("plugin error: %s", result.Error)
	}

	return &result, nil
}
