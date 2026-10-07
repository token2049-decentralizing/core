package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
	"github.com/token2049-decentralizing/core/cre-runner/internal/solana"
)

// evaluator runs one evaluation. Swapped for a fake in tests.
type evaluator interface {
	Evaluate(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error)
}

// cliSimulator runs `cre workflow simulate` against a prebuilt WASM binary.
type cliSimulator struct {
	Bin           string // cre CLI
	Dir           string // CRE project root (cre/)
	Workflow      string // workflow folder, relative to Dir
	Target        string
	Wasm          string // prebuilt binary; without it parallel runs race on the CLI's temp file
	Timeout       time.Duration
	Tokens        ghapp.Tokens
	ReviewerToken string // REVIEWER_TOKEN workflow secret; empty when reviewers are stubbed
	// Broadcast sends the workflow's chain writes (Solana payouts) for real; the CLI signs
	// them with CRE_SOLANA_PRIVATE_KEY.
	Broadcast bool
	// SolanaRPC becomes CRE_SOLANA_RPC_URL, which project.yaml uses for solana-devnet.
	SolanaRPC string
}

// evalError is a failure inside the workflow (bad PR, GitHub error...), not in the runner.
type evalError struct{ msg string }

func (e *evalError) Error() string { return e.msg }

func (s *cliSimulator) Evaluate(ctx context.Context, req *evaluationRequest, cfg *workflowConfig) (*evaluationResponse, error) {
	token, err := s.Tokens.ForRepo(req.Repository).Token(ctx)
	if err != nil {
		return nil, fmt.Errorf("github token: %w", err)
	}
	// Re-encoded: only validated fields reach the CLI.
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	// The campaign policy changes per execution, so each run gets its own config file.
	configPath, err := writeTempJSON(cfg)
	if err != nil {
		return nil, fmt.Errorf("workflow config: %w", err)
	}
	defer os.Remove(configPath)

	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()

	args := []string{"workflow", "simulate", s.Workflow, "--target", s.Target, "--config", configPath,
		"--non-interactive", "--trigger-index", "0", "--http-payload", string(payload)}
	if s.Wasm != "" {
		args = append(args, "--wasm", s.Wasm)
	}
	if s.Broadcast {
		args = append(args, "--broadcast")
	}
	cmd := exec.CommandContext(ctx, s.Bin, args...)
	cmd.Dir = s.Dir
	cmd.Env = append(childEnv(os.Environ()), "GITHUB_TOKEN_VALUE="+token)
	if s.ReviewerToken != "" {
		cmd.Env = append(cmd.Env, "REVIEWER_TOKEN_VALUE="+s.ReviewerToken)
	}
	cmd.Env = append(cmd.Env, "CRE_SOLANA_RPC_URL="+cmp.Or(s.SolanaRPC, solana.DevnetRPC))
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

func writeTempJSON(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	f, err := os.CreateTemp("", "cre-config-*.json")
	if err != nil {
		return "", err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return "", werr
	}
	return f.Name(), nil
}

// childEnv passes only what the CLI and workflow need. Runner secrets (Supabase key,
// webhook secret, App private key, LLM key) never reach the subprocess; the workflow's
// token secrets are set per run by Evaluate.
func childEnv(env []string) []string {
	keep := map[string]bool{"PATH": true, "HOME": true, "TMPDIR": true, "USER": true}
	var out []string
	drop := map[string]bool{"CRE_LOGIN_YAML": true, "CRE_CONTEXT_YAML": true} // Already installed in ~/.cre.
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		// An empty CRE_API_KEY must not shadow the login session.
		if drop[k] || (k == "CRE_API_KEY" && v == "") {
			continue
		}
		if keep[k] || strings.HasPrefix(k, "GO") || strings.HasPrefix(k, "CRE_") ||
			(strings.HasSuffix(k, "_VALUE") && k != "GITHUB_TOKEN_VALUE" && k != "REVIEWER_TOKEN_VALUE") {
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
