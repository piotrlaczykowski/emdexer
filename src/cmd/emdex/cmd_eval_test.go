package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadQuestions_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	body := `[
		{"question":"What is X?","expected_answer":"Y","namespace":"docs"},
		{"question":"What is Z?","expected_answer":"W"}
	]`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := loadQuestions(path)
	if err != nil {
		t.Fatalf("loadQuestions: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if got[0].Question != "What is X?" || got[0].ExpectedAnswer != "Y" || got[0].Namespace != "docs" {
		t.Errorf("entry[0] mismatch: %+v", got[0])
	}
	if got[1].Namespace != "" {
		t.Errorf("entry[1] namespace should be empty, got %q", got[1].Namespace)
	}
}

func TestLoadQuestions_BadJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	_ = os.WriteFile(path, []byte("{not json}"), 0o644)
	if _, err := loadQuestions(path); err == nil {
		t.Fatal("expected error for malformed JSON")
	}
}

func TestLoadQuestions_MissingFile(t *testing.T) {
	if _, err := loadQuestions("/definitely/not/here.json"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestCallEvalEndpoint_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("want POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/eval" {
			t.Errorf("want /v1/eval, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("auth header: want Bearer test-key, got %q", got)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req["question"] != "What is X?" {
			t.Errorf("request question mismatch: %v", req["question"])
		}
		if req["namespace"] != "docs" {
			t.Errorf("request namespace mismatch: %v", req["namespace"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"context_recall": 0.85,
			"faithfulness":   0.9,
			"latency_ms":     123,
		})
	}))
	defer srv.Close()

	q := evalQuestion{Question: "What is X?", ExpectedAnswer: "Y", Namespace: "docs"}
	resp, err := callEvalEndpoint(srv.URL, "test-key", q)
	if err != nil {
		t.Fatalf("callEvalEndpoint: %v", err)
	}
	if resp.ContextRecall != 0.85 {
		t.Errorf("recall: want 0.85, got %f", resp.ContextRecall)
	}
	if resp.LatencyMs != 123 {
		t.Errorf("latency: want 123, got %d", resp.LatencyMs)
	}
}

func TestCallEvalEndpoint_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	q := evalQuestion{Question: "?", ExpectedAnswer: "."}
	if _, err := callEvalEndpoint(srv.URL, "k", q); err == nil {
		t.Fatal("expected error on 500")
	}
}

func TestCallEvalEndpoint_MissingFields(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{})
	}))
	defer srv.Close()

	resp, err := callEvalEndpoint(srv.URL, "k", evalQuestion{Question: "?", ExpectedAnswer: "."})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ContextRecall != 0 || resp.Faithfulness != 0 {
		t.Errorf("missing fields should default to 0, got %+v", resp)
	}
}

func TestTruncateQuestion(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want string
	}{
		{"short", 40, "short"},
		{"this is a very long question that must get truncated eventually", 40, "this is a very long question that must g..."},
	}
	for _, tc := range cases {
		got := truncateQuestion(tc.in, tc.max)
		if got != tc.want {
			t.Errorf("truncateQuestion(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}

func TestPrintEvalTable_PassAndFail(t *testing.T) {
	outcomes := []evalOutcome{
		{Question: "What does the gateway do?", Verdict: "PASS", ContextRecall: 0.85, Faithfulness: 0.9, LatencyMs: 123},
		{Question: "What is the node?", Verdict: "FAIL", ContextRecall: 0.55, Faithfulness: 0.8, LatencyMs: 98},
	}
	var buf bytes.Buffer
	printEvalTable(&buf, outcomes, 0.7)
	out := buf.String()

	for _, want := range []string{
		"QUESTION", "VERDICT", "RECALL", "FAITHFUL", "LATENCY",
		"What does the gateway do?",
		"What is the node?",
		"PASS", "FAIL",
		"0.85", "0.55", "0.90", "0.80",
		"123ms", "98ms",
		"2 questions", "1 PASS", "1 FAIL",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("table output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestPrintEvalTable_TruncatesLongQuestions(t *testing.T) {
	long := strings.Repeat("x", 100)
	outcomes := []evalOutcome{{Question: long, Verdict: "PASS", ContextRecall: 0.9}}
	var buf bytes.Buffer
	printEvalTable(&buf, outcomes, 0.7)
	out := buf.String()
	if strings.Contains(out, strings.Repeat("x", 50)) {
		t.Errorf("expected truncation at 40 chars, got long line:\n%s", out)
	}
	if !strings.Contains(out, "...") {
		t.Errorf("expected truncation suffix '...', got:\n%s", out)
	}
}

func TestPrintEvalJSON_Structure(t *testing.T) {
	outcomes := []evalOutcome{
		{Question: "q1", Verdict: "PASS", ContextRecall: 0.85, Faithfulness: 0.9, LatencyMs: 123, Answer: "a1"},
		{Question: "q2", Verdict: "FAIL", ContextRecall: 0.5, Faithfulness: 0.8, LatencyMs: 98},
	}
	var buf bytes.Buffer
	if err := printEvalJSON(&buf, outcomes, 0.7); err != nil {
		t.Fatalf("printEvalJSON: %v", err)
	}

	var got struct {
		Total     int           `json:"total"`
		Passed    int           `json:"passed"`
		Failed    int           `json:"failed"`
		Threshold float64       `json:"threshold"`
		Results   []evalOutcome `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal output: %v\n%s", err, buf.String())
	}
	if got.Total != 2 || got.Passed != 1 || got.Failed != 1 {
		t.Errorf("counts: total=%d passed=%d failed=%d", got.Total, got.Passed, got.Failed)
	}
	if got.Threshold != 0.7 {
		t.Errorf("threshold: want 0.7, got %f", got.Threshold)
	}
	if len(got.Results) != 2 || got.Results[0].Question != "q1" {
		t.Errorf("results: %+v", got.Results)
	}
}

func TestParseEvalFlags_FileMode(t *testing.T) {
	opts, err := parseEvalFlags([]string{"--file=q.json", "--namespace=docs", "--threshold=0.8", "--output=json"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.file != "q.json" || opts.namespace != "docs" || opts.threshold != 0.8 || opts.output != "json" {
		t.Errorf("parsed wrong: %+v", opts)
	}
}

func TestParseEvalFlags_ShortForms(t *testing.T) {
	opts, err := parseEvalFlags([]string{"-f", "q.json", "-n", "docs", "-t", "0.5", "-o", "table"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.file != "q.json" || opts.namespace != "docs" || opts.threshold != 0.5 || opts.output != "table" {
		t.Errorf("parsed wrong: %+v", opts)
	}
}

func TestParseEvalFlags_SingleQuestion(t *testing.T) {
	opts, err := parseEvalFlags([]string{"--question=What is X?", "--expected=Y"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.question != "What is X?" || opts.expected != "Y" {
		t.Errorf("parsed wrong: %+v", opts)
	}
}

func TestParseEvalFlags_Defaults(t *testing.T) {
	opts, err := parseEvalFlags([]string{"--question=q", "--expected=a"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.threshold != 0.7 {
		t.Errorf("default threshold: want 0.7, got %f", opts.threshold)
	}
	if opts.output != "table" {
		t.Errorf("default output: want table, got %q", opts.output)
	}
	if opts.namespace != "default" {
		t.Errorf("default namespace: want default, got %q", opts.namespace)
	}
}

func TestParseEvalFlags_InvalidThreshold(t *testing.T) {
	if _, err := parseEvalFlags([]string{"--file=q.json", "--threshold=bogus"}); err == nil {
		t.Error("expected error for non-numeric threshold")
	}
	if _, err := parseEvalFlags([]string{"--file=q.json", "--threshold=1.5"}); err == nil {
		t.Error("expected error for threshold > 1")
	}
	if _, err := parseEvalFlags([]string{"--file=q.json", "--threshold=-0.1"}); err == nil {
		t.Error("expected error for threshold < 0")
	}
}

func TestParseEvalFlags_InvalidOutput(t *testing.T) {
	if _, err := parseEvalFlags([]string{"--file=q.json", "--output=yaml"}); err == nil {
		t.Error("expected error for unknown output format")
	}
}

// testEnv returns a closure that mimics os.Getenv for injection into runEval.
func testEnv(pairs ...string) func(string) string {
	m := make(map[string]string)
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(k string) string { return m[k] }
}

// newEvalServer returns an httptest server returning the given response body for every request.
func newEvalServer(t *testing.T, wantAuth string, body map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" && r.Header.Get("Authorization") != "Bearer "+wantAuth {
			t.Errorf("auth header mismatch: %q", r.Header.Get("Authorization"))
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
}

func TestEvalCmd_SinglePass(t *testing.T) {
	srv := newEvalServer(t, "k", map[string]any{
		"context_recall": 0.9, "faithfulness": 0.9, "latency_ms": 50,
	})
	defer srv.Close()

	var out, errBuf bytes.Buffer
	code := runEval(
		[]string{"--question=q", "--expected=a"},
		&out, &errBuf,
		testEnv("EMDEX_GATEWAY_URL", srv.URL, "EMDEX_AUTH_KEY", "k"),
	)
	if code != 0 {
		t.Errorf("exit code: want 0, got %d\nstdout: %s\nstderr: %s", code, out.String(), errBuf.String())
	}
	if !strings.Contains(out.String(), "PASS") {
		t.Errorf("expected PASS in output:\n%s", out.String())
	}
}

func TestEvalCmd_SingleFail(t *testing.T) {
	srv := newEvalServer(t, "k", map[string]any{
		"context_recall": 0.5, "faithfulness": 0.9, "latency_ms": 50,
	})
	defer srv.Close()

	var out, errBuf bytes.Buffer
	code := runEval(
		[]string{"--question=q", "--expected=a", "--threshold=0.7"},
		&out, &errBuf,
		testEnv("EMDEX_GATEWAY_URL", srv.URL, "EMDEX_AUTH_KEY", "k"),
	)
	if code != 1 {
		t.Errorf("exit code: want 1, got %d\nstdout: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "FAIL") {
		t.Errorf("expected FAIL in output:\n%s", out.String())
	}
}

func TestEvalCmd_FileInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		body := map[string]any{"faithfulness": 0.9, "latency_ms": 50}
		if req["question"] == "pass?" {
			body["context_recall"] = 0.9
		} else {
			body["context_recall"] = 0.3
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	_ = os.WriteFile(path, []byte(`[
		{"question":"pass?","expected_answer":"y"},
		{"question":"fail?","expected_answer":"y"}
	]`), 0o644)

	var out, errBuf bytes.Buffer
	code := runEval(
		[]string{"--file=" + path, "--threshold=0.7"},
		&out, &errBuf,
		testEnv("EMDEX_GATEWAY_URL", srv.URL, "EMDEX_AUTH_KEY", "k"),
	)
	if code != 1 {
		t.Errorf("exit code: want 1, got %d\nstdout: %s", code, out.String())
	}
	if !strings.Contains(out.String(), "1 PASS") || !strings.Contains(out.String(), "1 FAIL") {
		t.Errorf("expected 1 PASS | 1 FAIL summary:\n%s", out.String())
	}
}

func TestEvalCmd_JSONOutput(t *testing.T) {
	srv := newEvalServer(t, "k", map[string]any{
		"context_recall": 0.9, "faithfulness": 0.9, "latency_ms": 50,
	})
	defer srv.Close()

	var out, errBuf bytes.Buffer
	code := runEval(
		[]string{"--question=q", "--expected=a", "--output=json"},
		&out, &errBuf,
		testEnv("EMDEX_GATEWAY_URL", srv.URL, "EMDEX_AUTH_KEY", "k"),
	)
	if code != 0 {
		t.Fatalf("exit code: want 0, got %d\n%s", code, out.String())
	}

	var got struct {
		Total   int `json:"total"`
		Passed  int `json:"passed"`
		Failed  int `json:"failed"`
		Results []struct {
			Verdict string  `json:"verdict"`
			Recall  float64 `json:"context_recall"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("JSON output is not valid: %v\n%s", err, out.String())
	}
	if got.Total != 1 || got.Passed != 1 || got.Failed != 0 {
		t.Errorf("counts wrong: %+v", got)
	}
}

func TestEvalCmd_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	_ = os.WriteFile(path, []byte("[]"), 0o644)

	var out, errBuf bytes.Buffer
	code := runEval(
		[]string{"--file=" + path},
		&out, &errBuf,
		testEnv("EMDEX_GATEWAY_URL", "http://ignored", "EMDEX_AUTH_KEY", "k"),
	)
	if code != 2 {
		t.Errorf("exit code: want 2, got %d\nstderr: %s", code, errBuf.String())
	}
}

func TestEvalCmd_MissingAuthKey(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := runEval(
		[]string{"--question=q", "--expected=a"},
		&out, &errBuf,
		testEnv("EMDEX_GATEWAY_URL", "http://ignored"),
	)
	if code != 2 {
		t.Errorf("exit code: want 2, got %d\nstderr: %s", code, errBuf.String())
	}
}

func TestEvalCmd_PerEntryNamespaceOverridesFlag(t *testing.T) {
	var gotNS string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotNS, _ = req["namespace"].(string)
		_ = json.NewEncoder(w).Encode(map[string]any{"context_recall": 0.9, "faithfulness": 0.9})
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "q.json")
	_ = os.WriteFile(path, []byte(`[{"question":"q","expected_answer":"a","namespace":"from-entry"}]`), 0o644)

	var out, errBuf bytes.Buffer
	_ = runEval(
		[]string{"--file=" + path, "--namespace=from-flag"},
		&out, &errBuf,
		testEnv("EMDEX_GATEWAY_URL", srv.URL, "EMDEX_AUTH_KEY", "k"),
	)
	if gotNS != "from-entry" {
		t.Errorf("namespace: per-entry should win; got %q", gotNS)
	}
}
