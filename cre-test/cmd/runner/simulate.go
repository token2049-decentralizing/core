package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type tokenSource interface {
	Token(ctx context.Context) (string, error)
}

type staticToken string

func (s staticToken) Token(context.Context) (string, error) { return string(s), nil }

// evaluator runs one evaluation. Swapped for a fake in tests.
type evaluator interface {
	Evaluate(ctx context.Context, payload []byte) (*evaluationResponse, error)
}

// cliSimulator runs `cre workflow simulate` against a prebuilt WASM binary.
type cliSimulator struct {
	Bin      string // cre CLI
	Dir      string // CRE project root (cre-test/)
	Workflow string // workflow folder, relative to Dir
	Target   string
	Wasm     string // prebuilt binary; without it parallel runs race on the CLI's temp file
	Timeout  time.Duration
	Tokens   tokenSource
}

// evalError is a failure inside the workflow (bad PR, GitHub error...), not in the runner.
type evalError struct{ msg string }

func (e *evalError) Error() string { return e.msg }

func (s *cliSimulator) Evaluate(ctx context.Context, payload []byte) (*evaluationResponse, error) {
	token, err := s.Tokens.Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	args := []string{"workflow", "simulate", s.Workflow, "--target", s.Target,
		"--non-interactive", "--trigger-index", "0", "--http-payload", string(payload)}
	if s.Wasm != "" {
		args = append(args, "--wasm", s.Wasm)
	}
	cmd := exec.CommandContext(ctx, s.Bin, args...)
	cmd.Dir = s.Dir
	cmd.Env = append(childEnv(os.Environ()), "GITHUB_TOKEN_VALUE="+token)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out

	runErr := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, context.DeadlineExceeded
	}
	res, err := parseSimulateOutput(out.String())
	if err == nil {
		return res, nil
	}
	// CLI could not start at all (missing binary etc.): a runner problem, not an evaluation result.
	var exitErr *exec.ExitError
	if runErr != nil && !errors.As(runErr, &exitErr) {
		return nil, fmt.Errorf("run cre: %w", runErr)
	}
	return nil, err
}

// childEnv passes only what the CLI and workflow need.
// Runner secrets (shared secret, App private key) never reach the subprocess.
func childEnv(env []string) []string {
	keep := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "USER": true}
	var out []string
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if keep[k] || strings.HasPrefix(k, "GO") || strings.HasPrefix(k, "CRE_") ||
			(strings.HasSuffix(k, "_VALUE") && k != "GITHUB_TOKEN_VALUE") {
			out = append(out, kv)
		}
	}
	return out
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

const resultMarker = "Workflow Simulation Result:"

// parseSimulateOutput extracts the workflow's JSON result, or the CLI's error line.
func parseSimulateOutput(out string) (*evaluationResponse, error) {
	lines := strings.Split(ansiRe.ReplaceAllString(out, ""), "\n")
	for i, line := range lines {
		if !strings.Contains(line, resultMarker) {
			continue
		}
		for _, next := range lines[i+1:] {
			next = strings.TrimSpace(next)
			if next == "" {
				continue
			}
			var inner string // The CLI prints the returned string JSON-quoted.
			if err := json.Unmarshal([]byte(next), &inner); err != nil {
				return nil, fmt.Errorf("unexpected result line: %q", next)
			}
			var res evaluationResponse
			if err := json.Unmarshal([]byte(inner), &res); err != nil || res.EvaluationHash == "" {
				return nil, fmt.Errorf("unexpected result: %q", inner)
			}
			return &res, nil
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if msg, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "✗ "); ok {
			return nil, &evalError{msg: msg}
		}
	}
	return nil, &evalError{msg: "no result in simulator output: " + tail(lines, 5)}
}

func tail(lines []string, n int) string {
	var keep []string
	for i := len(lines) - 1; i >= 0 && len(keep) < n; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			keep = append([]string{l}, keep...)
		}
	}
	return strings.Join(keep, " | ")
}
