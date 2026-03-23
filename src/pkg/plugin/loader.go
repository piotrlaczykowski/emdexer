package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	pluginCallsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "emdexer_node_plugin_calls_total",
		Help: "Total extractor plugin calls partitioned by plugin name, file extension, and outcome (ok/error/timeout).",
	}, []string{"plugin", "extension", "status"})

	pluginDurationMs = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "emdexer_node_plugin_duration_ms",
		Help:    "Extractor plugin call duration in milliseconds.",
		Buckets: []float64{10, 50, 100, 500, 1000, 5000, 10000},
	}, []string{"plugin"})
)

// pythonCmd holds the path to the Python interpreter found at package init.
// Unexported so tests in the same package can override it to simulate a missing Python.
var pythonCmd string

func init() {
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			pythonCmd = p
			return
		}
	}
}

// ─── subprocess runner ───────────────────────────────────────────────────────

// pythonPlugin wraps a single .py script as an ExtractorPlugin.
type pythonPlugin struct {
	name       string
	extensions []string
	scriptPath string
	timeout    time.Duration
}

func (p *pythonPlugin) Name() string         { return p.name }
func (p *pythonPlugin) Extensions() []string { return p.extensions }

// Extract runs the Python script as a subprocess, passes file content via stdin
// as base64(JSON({"filename":..., "data":base64(bytes)})), and reads a JSON
// response {"text":..., "relations":[...]} from stdout.
func (p *pythonPlugin) Extract(ctx context.Context, filename string, data []byte) (string, []Relation, error) {
	start := time.Now()

	// Encode payload: base64( JSON( {filename, data: base64(bytes)} ) )
	inner := map[string]string{
		"filename": filename,
		"data":     base64.StdEncoding.EncodeToString(data),
	}
	innerJSON, err := json.Marshal(inner)
	if err != nil {
		return "", nil, fmt.Errorf("plugin %s: marshal payload: %w", p.name, err)
	}
	stdinPayload := base64.StdEncoding.EncodeToString(innerJSON)

	// Apply per-call deadline on top of any parent context deadline.
	callCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	cmd := exec.CommandContext(callCtx, pythonCmd, p.scriptPath)
	cmd.Stdin = strings.NewReader(stdinPayload)

	out, execErr := cmd.Output()

	// Record Prometheus metrics.
	ext := strings.ToLower(filepath.Ext(filename))
	status := "ok"
	if execErr != nil {
		if callCtx.Err() != nil {
			status = "timeout"
		} else {
			status = "error"
		}
	}
	pluginCallsTotal.WithLabelValues(p.name, ext, status).Inc()
	pluginDurationMs.WithLabelValues(p.name).Observe(float64(time.Since(start).Milliseconds()))

	if execErr != nil {
		return "", nil, fmt.Errorf("plugin %s: subprocess: %w", p.name, execErr)
	}

	var resp struct {
		Text      string     `json:"text"`
		Relations []Relation `json:"relations"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return "", nil, fmt.Errorf("plugin %s: invalid JSON response: %w", p.name, err)
	}

	return resp.Text, resp.Relations, nil
}

// ─── HTTP sidecar client ─────────────────────────────────────────────────────

// sidecarPlugin calls the plugin-sidecar HTTP service for a specific plugin.
type sidecarPlugin struct {
	name       string
	extensions []string
	extractURL string // full URL, e.g. http://plugin-sidecar:8003/extract
	httpClient *http.Client
}

func (s *sidecarPlugin) Name() string         { return s.name }
func (s *sidecarPlugin) Extensions() []string { return s.extensions }

// Extract uploads the file to the plugin sidecar via multipart POST and
// returns the JSON response {"text":..., "relations":[...]}.
func (s *sidecarPlugin) Extract(ctx context.Context, filename string, data []byte) (string, []Relation, error) {
	start := time.Now()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return "", nil, fmt.Errorf("sidecar plugin %s: create form file: %w", s.name, err)
	}
	if _, err = fw.Write(data); err != nil {
		return "", nil, fmt.Errorf("sidecar plugin %s: write form file: %w", s.name, err)
	}
	mw.Close()

	reqURL := s.extractURL + "?plugin=" + url.QueryEscape(s.name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &body)
	if err != nil {
		return "", nil, fmt.Errorf("sidecar plugin %s: build request: %w", s.name, err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, httpErr := s.httpClient.Do(req)

	ext := strings.ToLower(filepath.Ext(filename))
	status := "ok"
	if httpErr != nil {
		status = "error"
	} else if resp.StatusCode >= 400 {
		status = "error"
	}
	pluginCallsTotal.WithLabelValues(s.name, ext, status).Inc()
	pluginDurationMs.WithLabelValues(s.name).Observe(float64(time.Since(start).Milliseconds()))

	if httpErr != nil {
		return "", nil, fmt.Errorf("sidecar plugin %s: HTTP request: %w", s.name, httpErr)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("sidecar plugin %s: server error %d: %s", s.name, resp.StatusCode, strings.TrimSpace(string(msg)))
	}

	var result struct {
		Text      string     `json:"text"`
		Relations []Relation `json:"relations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", nil, fmt.Errorf("sidecar plugin %s: decode response: %w", s.name, err)
	}

	return result.Text, result.Relations, nil
}

// loadFromSidecar queries GET {base}/plugins to discover registered plugins
// and returns one sidecarPlugin per entry.
//
// extractURL is the full /extract endpoint URL, e.g. http://plugin-sidecar:8003/extract.
// The base URL is derived by stripping the "/extract" suffix.
func loadFromSidecar(extractURL string) ([]ExtractorPlugin, error) {
	baseURL := strings.TrimSuffix(extractURL, "/extract")
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(baseURL + "/plugins")
	if err != nil {
		return nil, fmt.Errorf("plugin sidecar: GET /plugins: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("plugin sidecar: GET /plugins returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var list []struct {
		Name       string   `json:"name"`
		Extensions []string `json:"extensions"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("plugin sidecar: decode /plugins: %w", err)
	}

	plugins := make([]ExtractorPlugin, 0, len(list))
	for _, entry := range list {
		plugins = append(plugins, &sidecarPlugin{
			name:       entry.Name,
			extensions: entry.Extensions,
			extractURL: extractURL,
			httpClient: client,
		})
		log.Printf("[plugin] sidecar: registered %s for %v", entry.Name, entry.Extensions)
	}
	return plugins, nil
}

// loadFromDir scans dir for *.py files, parses their metadata, and returns
// pythonPlugin instances. Called when no sidecar URL is configured.
func loadFromDir(dir string) ([]ExtractorPlugin, error) {
	if pythonCmd == "" {
		log.Printf("[plugin] Python not found on PATH — skipping plugin loading from %s", dir)
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("plugin: read dir %s: %w", dir, err)
	}

	// Parse EMDEX_PLUGIN_TIMEOUT once for all plugins in this dir.
	timeout := 10 * time.Second
	if s := os.Getenv("EMDEX_PLUGIN_TIMEOUT"); s != "" {
		if d, parseErr := time.ParseDuration(s); parseErr == nil {
			timeout = d
		} else {
			log.Printf("[plugin] Invalid EMDEX_PLUGIN_TIMEOUT=%q — using default 10s", s)
		}
	}

	extToPluginName := map[string]string{} // tracks which plugin last claimed each ext
	var plugins []ExtractorPlugin

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".py") {
			continue
		}
		scriptPath := filepath.Join(dir, entry.Name())
		src, readErr := os.ReadFile(scriptPath)
		if readErr != nil {
			log.Printf("[plugin] Failed to read %s: %v — skipping", scriptPath, readErr)
			continue
		}

		name, exts := parsePluginMeta(string(src))
		if name == "" || len(exts) == 0 {
			log.Printf("[plugin] Skipping %s — missing # name: or # extensions: metadata", entry.Name())
			continue
		}

		p := &pythonPlugin{
			name:       name,
			extensions: exts,
			scriptPath: scriptPath,
			timeout:    timeout,
		}

		for _, ext := range exts {
			if prev, ok := extToPluginName[ext]; ok {
				log.Printf("[plugin] WARN: extension %s claimed by %q and %q — last-loaded (%q) wins", ext, prev, name, name)
			}
			extToPluginName[ext] = name
		}

		plugins = append(plugins, p)
		log.Printf("[plugin] loaded: %s for %v", name, exts)
	}

	return plugins, nil
}

// LoadPlugins returns a ready-to-use set of ExtractorPlugin values.
//
//   - If EMDEX_PLUGIN_SIDECAR_URL is set, plugins are discovered from the
//     plugin-sidecar HTTP service at that URL (production path).
//   - Otherwise the dir is scanned for *.py files and plugins are run as
//     subprocesses (dev/testing path; requires Python on PATH).
//
// Returns nil, nil (no error) when no sidecar is configured and Python is
// unavailable, or when dir does not exist.
func LoadPlugins(dir string) ([]ExtractorPlugin, error) {
	if url := os.Getenv("EMDEX_PLUGIN_SIDECAR_URL"); url != "" {
		log.Printf("[plugin] Using sidecar at %s", url)
		return loadFromSidecar(url)
	}
	return loadFromDir(dir)
}

// parsePluginMeta scans the first 30 lines of a Python plugin source for
// # name: <name> and # extensions: <ext1>,<ext2>,... metadata comments.
func parsePluginMeta(src string) (name string, exts []string) {
	scanner := bufio.NewScanner(strings.NewReader(src))
	for i := 0; scanner.Scan() && i < 30; i++ {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "#") {
			continue
		}
		if after, ok := strings.CutPrefix(line, "# name:"); ok {
			name = strings.TrimSpace(after)
		} else if after, ok := strings.CutPrefix(line, "# extensions:"); ok {
			for _, e := range strings.Split(strings.TrimSpace(after), ",") {
				if e = strings.TrimSpace(e); e != "" {
					exts = append(exts, e)
				}
			}
		}
	}
	return
}
