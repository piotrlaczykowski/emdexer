package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// validPluginSrc is a minimal but working plugin for testing.
const validPluginSrc = `# name: Test Plugin
# extensions: .test,.tst

import json, sys, base64

def extract(filename, data):
    return {"text": "extracted: " + filename, "relations": []}

if __name__ == '__main__':
    payload = json.loads(base64.b64decode(sys.stdin.read()))
    print(json.dumps(extract(payload['filename'], base64.b64decode(payload['data']))))
`

// slowPluginSrc is a plugin that sleeps for 30 seconds — used for timeout testing.
const slowPluginSrc = `# name: Slow Plugin
# extensions: .slow

import time, json, sys, base64

def extract(filename, data):
    time.sleep(30)
    return {"text": "never", "relations": []}

if __name__ == '__main__':
    payload = json.loads(base64.b64decode(sys.stdin.read()))
    print(json.dumps(extract(payload['filename'], base64.b64decode(payload['data']))))
`

func writePlugin(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatalf("writePlugin: %v", err)
	}
	return path
}

// TestLoadPlugins_ValidPlugin verifies that a well-formed plugin is loaded with
// the correct Name() and Extensions() values.
func TestLoadPlugins_ValidPlugin(t *testing.T) {
	if pythonCmd == "" {
		t.Skip("python not available on this system")
	}
	dir := t.TempDir()
	writePlugin(t, dir, "test_plugin.py", validPluginSrc)

	plugins, err := LoadPlugins(dir)
	if err != nil {
		t.Fatalf("LoadPlugins error: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	p := plugins[0]
	if p.Name() != "Test Plugin" {
		t.Errorf("Name() = %q, want %q", p.Name(), "Test Plugin")
	}
	exts := p.Extensions()
	if len(exts) != 2 || exts[0] != ".test" || exts[1] != ".tst" {
		t.Errorf("Extensions() = %v, want [.test .tst]", exts)
	}
}

// TestLoadPlugins_NoPythonGraceful verifies that when Python is unavailable the
// loader returns an empty slice without panicking or returning an error.
func TestLoadPlugins_NoPythonGraceful(t *testing.T) {
	// Temporarily hide the Python binary to simulate a system without Python.
	original := pythonCmd
	pythonCmd = ""
	defer func() { pythonCmd = original }()

	dir := t.TempDir()
	writePlugin(t, dir, "test_plugin.py", validPluginSrc)

	plugins, err := LoadPlugins(dir)
	if err != nil {
		t.Fatalf("expected nil error when Python is unavailable, got: %v", err)
	}
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins when Python is unavailable, got %d", len(plugins))
	}
}

// TestLoadPlugins_DuplicateExtensionWarn verifies that when two plugins declare
// the same extension, both are loaded (last-loaded wins the extension) and no
// error is returned.
func TestLoadPlugins_DuplicateExtensionWarn(t *testing.T) {
	if pythonCmd == "" {
		t.Skip("python not available on this system")
	}
	dir := t.TempDir()

	const pluginA = `# name: Plugin A
# extensions: .dup

import json, sys, base64

def extract(filename, data):
    return {"text": "from A", "relations": []}

if __name__ == '__main__':
    payload = json.loads(base64.b64decode(sys.stdin.read()))
    print(json.dumps(extract(payload['filename'], base64.b64decode(payload['data']))))
`
	const pluginB = `# name: Plugin B
# extensions: .dup

import json, sys, base64

def extract(filename, data):
    return {"text": "from B", "relations": []}

if __name__ == '__main__':
    payload = json.loads(base64.b64decode(sys.stdin.read()))
    print(json.dumps(extract(payload['filename'], base64.b64decode(payload['data']))))
`
	// Use alphabetical names so directory order is deterministic.
	writePlugin(t, dir, "a_plugin.py", pluginA)
	writePlugin(t, dir, "b_plugin.py", pluginB)

	plugins, err := LoadPlugins(dir)
	if err != nil {
		t.Fatalf("LoadPlugins error: %v", err)
	}
	// Both plugins must be returned even though they share an extension.
	if len(plugins) != 2 {
		t.Fatalf("expected 2 plugins, got %d", len(plugins))
	}
	names := map[string]bool{}
	for _, p := range plugins {
		names[p.Name()] = true
	}
	if !names["Plugin A"] || !names["Plugin B"] {
		t.Errorf("unexpected plugin names: %v", names)
	}
}

// TestPluginClient_Sidecar verifies that loadFromSidecar discovers plugins from
// GET /plugins and that the returned sidecarPlugin sends multipart POST /extract
// requests and correctly parses the JSON response.
func TestPluginClient_Sidecar(t *testing.T) {
	// Fake sidecar server.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/plugins":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode([]map[string]any{
				{"name": "CSV Extractor", "extensions": []string{".csv"}},
			})
		case "/extract":
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"text":      "col1,col2",
				"relations": []any{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Point loadFromSidecar at the test server.
	plugins, err := loadFromSidecar(srv.URL + "/extract")
	if err != nil {
		t.Fatalf("loadFromSidecar error: %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin from sidecar, got %d", len(plugins))
	}
	p := plugins[0]
	if p.Name() != "CSV Extractor" {
		t.Errorf("Name() = %q, want %q", p.Name(), "CSV Extractor")
	}
	if len(p.Extensions()) != 1 || p.Extensions()[0] != ".csv" {
		t.Errorf("Extensions() = %v, want [.csv]", p.Extensions())
	}

	// Verify Extract calls /extract and parses the response.
	text, rels, err := p.Extract(context.Background(), "data.csv", []byte("col1,col2\n1,2"))
	if err != nil {
		t.Fatalf("Extract error: %v", err)
	}
	if text != "col1,col2" {
		t.Errorf("text = %q, want %q", text, "col1,col2")
	}
	if len(rels) != 0 {
		t.Errorf("relations = %v, want empty", rels)
	}
}

// TestPluginClient_FallbackToSubprocess verifies that when EMDEX_PLUGIN_SIDECAR_URL
// is not set, LoadPlugins falls back to the subprocess path (loadFromDir).
func TestPluginClient_FallbackToSubprocess(t *testing.T) {
	// Ensure no sidecar URL is set.
	t.Setenv("EMDEX_PLUGIN_SIDECAR_URL", "")

	// With Python unavailable the subprocess path should return 0 plugins gracefully.
	original := pythonCmd
	pythonCmd = ""
	defer func() { pythonCmd = original }()

	dir := t.TempDir()
	writePlugin(t, dir, "test_plugin.py", validPluginSrc)

	plugins, err := LoadPlugins(dir)
	if err != nil {
		t.Fatalf("LoadPlugins error: %v", err)
	}
	// Subprocess path with no Python → 0 plugins, no error.
	if len(plugins) != 0 {
		t.Fatalf("expected 0 plugins (no Python, no sidecar), got %d", len(plugins))
	}
}

// TestPluginExtract_Timeout verifies that a subprocess that exceeds the plugin
// timeout is killed and Extract returns an error promptly.
func TestPluginExtract_Timeout(t *testing.T) {
	if pythonCmd == "" {
		t.Skip("python not available on this system")
	}
	dir := t.TempDir()
	scriptPath := writePlugin(t, dir, "slow.py", slowPluginSrc)

	p := &pythonPlugin{
		name:       "Slow Plugin",
		extensions: []string{".slow"},
		scriptPath: scriptPath,
		timeout:    200 * time.Millisecond,
	}

	start := time.Now()
	_, _, err := p.Extract(context.Background(), "test.slow", []byte("hello"))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error due to timeout, got nil")
	}
	// The call must finish well within 2 seconds despite the 30-second sleep.
	if elapsed > 2*time.Second {
		t.Errorf("Extract took %v — timeout enforcement too slow", elapsed)
	}
}
