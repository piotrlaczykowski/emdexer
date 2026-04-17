package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// evalQuestion is one entry in the input JSON file, or a single --question invocation.
type evalQuestion struct {
	Question       string `json:"question"`
	ExpectedAnswer string `json:"expected_answer"`
	Namespace      string `json:"namespace,omitempty"`
}

// evalResponse mirrors the subset of eval.Result the CLI cares about.
// The gateway's handler returns these fields (see src/pkg/eval/eval.go).
// Any field the gateway omits stays at its zero value.
type evalResponse struct {
	ContextRecall float64 `json:"context_recall"`
	Faithfulness  float64 `json:"faithfulness"`
	LatencyMs     int64   `json:"latency_ms"`
	Answer        string  `json:"answer,omitempty"` // not returned by gateway today; tolerate absence
	Error         string  `json:"error,omitempty"`
}

// evalOutcome is what we render per question in the CLI.
type evalOutcome struct {
	Question      string  `json:"question"`
	Verdict       string  `json:"verdict"` // "PASS" | "FAIL"
	ContextRecall float64 `json:"context_recall"`
	Faithfulness  float64 `json:"faithfulness"`
	LatencyMs     int64   `json:"latency_ms"`
	Answer        string  `json:"answer"`
	Error         string  `json:"error,omitempty"`
}

// evalOpts holds parsed CLI options.
type evalOpts struct {
	file      string
	question  string
	expected  string
	namespace string
	threshold float64
	output    string
	help      bool
}

// Exit codes (CI-friendly).
const (
	exitOK          = 0
	exitFailures    = 1
	exitConfigError = 2
)

// cmdEval is the entry point for `emdex eval`. It delegates to runEval
// and exits with the returned code so runEval can be unit-tested.
func cmdEval() {
	os.Exit(runEval(os.Args[2:], os.Stdout, os.Stderr, os.Getenv))
}

// loadQuestions reads a JSON file containing an array of evalQuestion objects.
func loadQuestions(path string) ([]evalQuestion, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open eval file: %w", err)
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read eval file: %w", err)
	}

	var qs []evalQuestion
	if err := json.Unmarshal(data, &qs); err != nil {
		return nil, fmt.Errorf("parse eval file: %w", err)
	}
	return qs, nil
}

// callEvalEndpoint POSTs one question to {gatewayURL}/v1/eval and decodes the response.
// It returns an error for transport failures, non-200 status codes, or malformed JSON.
func callEvalEndpoint(gatewayURL, authKey string, q evalQuestion) (evalResponse, error) {
	body := map[string]any{
		"question":        q.Question,
		"expected_answer": q.ExpectedAnswer,
		"namespace":       q.Namespace,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return evalResponse{}, fmt.Errorf("marshal eval request: %w", err)
	}

	// Long timeout — eval runs two LLM calls per question (recall + faithfulness).
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodPost, gatewayURL+"/v1/eval", bytes.NewReader(bodyBytes))
	if err != nil {
		return evalResponse{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+authKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return evalResponse{}, fmt.Errorf("gateway unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return evalResponse{}, fmt.Errorf("HTTP %d from gateway", resp.StatusCode)
	}

	var er evalResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return evalResponse{}, fmt.Errorf("decode eval response: %w", err)
	}
	return er, nil
}

// truncateQuestion shortens a question to at most max runes, appending "..." when cut.
// Input shorter than or equal to max is returned unchanged. Operates on runes so
// multibyte characters are not split.
func truncateQuestion(q string, max int) string {
	r := []rune(q)
	if len(r) <= max {
		return q
	}
	return string(r[:max]) + "..."
}

// printEvalTable writes a human-readable table of eval outcomes to w.
// Columns: QUESTION(43) VERDICT(7) RECALL(6) FAITHFUL(8) LATENCY.
func printEvalTable(w io.Writer, outcomes []evalOutcome, threshold float64) {
	fmt.Fprintf(w, "\n  %s  %s  %s  %s  %s\n",
		padRight("QUESTION", 43),
		padRight("VERDICT", 7),
		padRight("RECALL", 6),
		padRight("FAITHFUL", 8),
		"LATENCY",
	)
	fmt.Fprintf(w, "  %s\n", strings.Repeat("-", 78))

	passed, failed := 0, 0
	for _, o := range outcomes {
		if o.Verdict == "PASS" {
			passed++
		} else {
			failed++
		}
		fmt.Fprintf(w, "  %s  %s  %s  %s  %dms\n",
			padRight(truncateQuestion(o.Question, 40), 43),
			padRight(o.Verdict, 7),
			fmt.Sprintf("%-6.2f", o.ContextRecall),
			fmt.Sprintf("%-8.2f", o.Faithfulness),
			o.LatencyMs,
		)
	}

	fmt.Fprintf(w, "\n  %d questions | %d PASS | %d FAIL  (threshold=%.2f)\n\n",
		len(outcomes), passed, failed, threshold)
}

// padRight pads s with spaces on the right to reach width.
// If s is already at or past width it is returned as-is.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// printEvalJSON writes a machine-readable summary to w.
// Output schema: {total, passed, failed, threshold, results[]}.
func printEvalJSON(w io.Writer, outcomes []evalOutcome, threshold float64) error {
	passed, failed := 0, 0
	for _, o := range outcomes {
		if o.Verdict == "PASS" {
			passed++
		} else {
			failed++
		}
	}
	summary := struct {
		Total     int           `json:"total"`
		Passed    int           `json:"passed"`
		Failed    int           `json:"failed"`
		Threshold float64       `json:"threshold"`
		Results   []evalOutcome `json:"results"`
	}{
		Total:     len(outcomes),
		Passed:    passed,
		Failed:    failed,
		Threshold: threshold,
		Results:   outcomes,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(summary)
}

// parseEvalFlags parses emdex-eval-style flags. It accepts both long and short forms
// (`--file`/`-f`) and both `--key=value` and `--key value` separators. Returns a
// descriptive error for unknown flags, bad numeric values, or threshold out of [0,1].
func parseEvalFlags(args []string) (evalOpts, error) {
	opts := evalOpts{
		namespace: "default",
		threshold: 0.7,
		output:    "table",
	}

	// takeValue pulls --key=value inline or consumes the next arg as value.
	takeValue := func(i int, flag string) (string, int, error) {
		arg := args[i]
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			return arg[eq+1:], i, nil
		}
		if i+1 >= len(args) {
			return "", i, fmt.Errorf("flag %s requires a value", flag)
		}
		return args[i+1], i + 1, nil
	}

	matches := func(arg, long, short string) bool {
		return arg == long || arg == short || strings.HasPrefix(arg, long+"=")
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			opts.help = true
		case matches(arg, "--file", "-f"):
			v, ni, err := takeValue(i, "--file")
			if err != nil {
				return opts, err
			}
			opts.file, i = v, ni
		case matches(arg, "--question", "-q"):
			v, ni, err := takeValue(i, "--question")
			if err != nil {
				return opts, err
			}
			opts.question, i = v, ni
		case matches(arg, "--expected", "-e"):
			v, ni, err := takeValue(i, "--expected")
			if err != nil {
				return opts, err
			}
			opts.expected, i = v, ni
		case matches(arg, "--namespace", "-n"):
			v, ni, err := takeValue(i, "--namespace")
			if err != nil {
				return opts, err
			}
			opts.namespace, i = v, ni
		case matches(arg, "--threshold", "-t"):
			v, ni, err := takeValue(i, "--threshold")
			if err != nil {
				return opts, err
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return opts, fmt.Errorf("invalid --threshold %q: %w", v, err)
			}
			if f < 0 || f > 1 {
				return opts, fmt.Errorf("--threshold must be between 0 and 1, got %v", f)
			}
			opts.threshold, i = f, ni
		case matches(arg, "--output", "-o"):
			v, ni, err := takeValue(i, "--output")
			if err != nil {
				return opts, err
			}
			if v != "table" && v != "json" {
				return opts, fmt.Errorf("--output must be 'table' or 'json', got %q", v)
			}
			opts.output, i = v, ni
		default:
			return opts, fmt.Errorf("unknown flag: %s", arg)
		}
	}
	return opts, nil
}

// runEval is the testable entry point. It returns a process exit code instead of
// calling os.Exit so that tests can inject args + env + stdout/stderr.
func runEval(args []string, stdout, stderr io.Writer, env func(string) string) int {
	opts, err := parseEvalFlags(args)
	if err != nil {
		fmt.Fprintf(stderr, "  X %s\n", err)
		printEvalHelp(stderr)
		return exitConfigError
	}
	if opts.help {
		printEvalHelp(stdout)
		return exitOK
	}

	// Validate input mode: exactly one of --file or --question.
	if (opts.file == "" && opts.question == "") || (opts.file != "" && opts.question != "") {
		fmt.Fprintf(stderr, "  X either --file or --question is required (not both)\n")
		return exitConfigError
	}
	if opts.question != "" && opts.expected == "" {
		fmt.Fprintf(stderr, "  X --expected is required when using --question\n")
		return exitConfigError
	}

	// Gateway URL + auth.
	gatewayURL := env("EMDEX_GATEWAY_URL")
	if gatewayURL == "" {
		gatewayURL = "http://localhost:7700"
	}
	authKey := env("EMDEX_AUTH_KEY")
	if authKey == "" {
		fmt.Fprintf(stderr, "  X EMDEX_AUTH_KEY is required\n")
		return exitConfigError
	}

	// Build the question list.
	var questions []evalQuestion
	if opts.file != "" {
		qs, err := loadQuestions(opts.file)
		if err != nil {
			fmt.Fprintf(stderr, "  X %s\n", err)
			return exitConfigError
		}
		if len(qs) == 0 {
			fmt.Fprintf(stderr, "  X eval file %q contains no questions\n", opts.file)
			return exitConfigError
		}
		questions = qs
	} else {
		questions = []evalQuestion{{
			Question:       opts.question,
			ExpectedAnswer: opts.expected,
		}}
	}

	// Run each question and assemble outcomes.
	outcomes := make([]evalOutcome, 0, len(questions))
	for _, q := range questions {
		ns := q.Namespace
		if ns == "" {
			ns = opts.namespace
		}
		q.Namespace = ns

		resp, err := callEvalEndpoint(gatewayURL, authKey, q)
		outcome := evalOutcome{
			Question:      q.Question,
			ContextRecall: resp.ContextRecall,
			Faithfulness:  resp.Faithfulness,
			LatencyMs:     resp.LatencyMs,
			Answer:        resp.Answer,
		}
		if err != nil {
			outcome.Verdict = "FAIL"
			outcome.Error = err.Error()
		} else if resp.ContextRecall >= opts.threshold {
			outcome.Verdict = "PASS"
		} else {
			outcome.Verdict = "FAIL"
		}
		outcomes = append(outcomes, outcome)
	}

	// Render.
	if opts.output == "json" {
		if err := printEvalJSON(stdout, outcomes, opts.threshold); err != nil {
			fmt.Fprintf(stderr, "  X render JSON: %s\n", err)
			return exitConfigError
		}
	} else {
		printEvalTable(stdout, outcomes, opts.threshold)
	}

	// Exit code: any FAIL -> 1.
	for _, o := range outcomes {
		if o.Verdict != "PASS" {
			return exitFailures
		}
	}
	return exitOK
}

// printEvalHelp renders the subcommand usage text.
func printEvalHelp(w io.Writer) {
	fmt.Fprintf(w, "\n  emdex eval [flags]\n\n")
	fmt.Fprintf(w, "  Flags:\n")
	fmt.Fprintf(w, "    --file, -f <path>         JSON file with an array of eval questions\n")
	fmt.Fprintf(w, "    --question, -q <text>     Single question (mutually exclusive with --file)\n")
	fmt.Fprintf(w, "    --expected, -e <text>     Expected answer (required with --question)\n")
	fmt.Fprintf(w, "    --namespace, -n <ns>      Namespace to query (default: 'default')\n")
	fmt.Fprintf(w, "    --threshold, -t <0..1>    Pass threshold on context_recall (default: 0.7)\n")
	fmt.Fprintf(w, "    --output, -o table|json   Output format (default: table)\n\n")
	fmt.Fprintf(w, "  Exit codes: 0=all pass  1=at least one fail  2=config error\n\n")
}
