package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"contriboracle/internal/ghapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const secret = "test-shared-secret-123"

func sign(body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func fixture(t *testing.T, name string) string {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(b)
}

var validBody = []byte(`{"repository":"acme/pool","pr_number":1,"campaign_id":"c","event":"opened"}`)

type fakeEval struct {
	fn func(ctx context.Context, payload []byte) (*evaluationResponse, error)
}

func (f fakeEval) Evaluate(ctx context.Context, p []byte) (*evaluationResponse, error) {
	return f.fn(ctx, p)
}

func okEval(context.Context, []byte) (*evaluationResponse, error) {
	return &evaluationResponse{Score: 30, Reward: "0", EvaluationHash: "0xabc", PolicyHash: "0xdef"}, nil
}

func newTestServer(fn func(context.Context, []byte) (*evaluationResponse, error), concurrency, queue int) *httptest.Server {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l, _ := openLedger("")
	return httptest.NewServer(newServer([]byte(secret), fakeEval{fn}, l, concurrency, queue, time.Second, log).routes())
}

// post is safe to call from goroutines (uses assert, not require).
func post(t *testing.T, url string, body []byte, sig string) (*http.Response, map[string]any) {
	req, _ := http.NewRequest(http.MethodPost, url+"/evaluate", bytes.NewReader(body))
	if sig != "" {
		req.Header.Set(signatureHeader, sig)
	}
	res, err := http.DefaultClient.Do(req)
	if !assert.NoError(t, err) {
		return &http.Response{}, nil
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res, out
}

func TestValidSignature(t *testing.T) {
	require.True(t, validSignature([]byte(secret), validBody, sign(validBody)))
	require.False(t, validSignature([]byte(secret), validBody, ""))
	require.False(t, validSignature([]byte(secret), validBody, "sha256=zz"))
	require.False(t, validSignature([]byte(secret), append(validBody, ' '), sign(validBody)))
	require.False(t, validSignature([]byte("other-secret-xxxxxx"), validBody, sign(validBody)))
}

func TestParseRequest(t *testing.T) {
	req, err := parseRequest([]byte(`{"repository":"a/b","pr_number":2,"campaign_id":"c","event":"merged","notes":{"k":1}}`))
	require.NoError(t, err)
	require.Equal(t, "a/b", req.Repository)
	require.Equal(t, map[string]any{"k": 1.0}, req.Notes)

	for _, bad := range []string{
		`[]`, `{"repository":"x","pr_number":1,"campaign_id":"c","event":"opened"}`,
		`{"repository":"a/b","pr_number":0,"campaign_id":"c","event":"opened"}`,
		`{"repository":"a/b","pr_number":1,"campaign_id":"","event":"opened"}`,
		`{"repository":"a/b","pr_number":1,"campaign_id":"c","event":"closed"}`,
		`{"repository":"a/b","pr_number":1,"campaign_id":"c","event":"opened","notes":null}`,
		`{"repository":"a/b","pr_number":1,"campaign_id":"c","event":"opened","head_sha":"xyz"}`,
	} {
		_, err := parseRequest([]byte(bad))
		require.Error(t, err, bad)
	}
}

func TestParseSimulateOutput(t *testing.T) {
	res, err := parseSimulateOutput(fixture(t, "success.txt"))
	require.NoError(t, err)
	require.Equal(t, &evaluationResponse{
		Score: 30, Eligible: false, Reward: "0",
		EvaluationHash: "0x4ea2c44568aa735bfbbcdd681e0d608ca289cc09eef856139c8ab875a7ed63f2",
		PolicyHash:     "0xbb5dc2d2ee4cfb3d3a20f48dc37c0a388e09e7e29576530d4c0e4f880e740ea1",
	}, res)

	// Colored output parses the same.
	_, err = parseSimulateOutput(strings.ReplaceAll(fixture(t, "success.txt"), "✓", "\x1b[32m✓\x1b[0m"))
	require.NoError(t, err)

	_, err = parseSimulateOutput(fixture(t, "failure.txt"))
	var ee *evalError
	require.ErrorAs(t, err, &ee)
	require.Contains(t, err.Error(), "HTTP 401")

	_, err = parseSimulateOutput("something odd\n")
	require.ErrorAs(t, err, &ee)
}

func TestChildEnvDropsRunnerSecrets(t *testing.T) {
	env := childEnv([]string{
		"PATH=/bin", "HOME=/home/app", "GOTOOLCHAIN=go1.25.3", "CRE_API_KEY=k", "REVIEWER_TOKEN_VALUE=r",
		"RUNNER_SHARED_SECRET=s", "GITHUB_APP_PRIVATE_KEY_PATH=/k.pem", "GITHUB_APP_ID=1", "GITHUB_TOKEN_VALUE=stale",
	})
	require.ElementsMatch(t, []string{
		"PATH=/bin", "HOME=/home/app", "GOTOOLCHAIN=go1.25.3", "CRE_API_KEY=k", "REVIEWER_TOKEN_VALUE=r",
	}, env)
}

func TestEvaluateHTTP(t *testing.T) {
	srv := newTestServer(okEval, 2, 2)
	defer srv.Close()

	res, out := post(t, srv.URL, validBody, sign(validBody))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, 30.0, out["score"])
	require.Equal(t, "0xabc", out["evaluation_hash"])

	res, _ = post(t, srv.URL, validBody, "")
	require.Equal(t, http.StatusUnauthorized, res.StatusCode)

	bad := []byte(`{"repository":"nope"}`)
	res, out = post(t, srv.URL, bad, sign(bad))
	require.Equal(t, http.StatusBadRequest, res.StatusCode)
	require.Contains(t, out["error"], "repository")

	h, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, h.StatusCode)
}

func TestEvaluateErrors(t *testing.T) {
	cases := map[int]error{
		http.StatusBadGateway:          &evalError{msg: "GitHub -> HTTP 404"},
		http.StatusGatewayTimeout:      context.DeadlineExceeded,
		http.StatusInternalServerError: io.ErrUnexpectedEOF,
	}
	for status, err := range cases {
		srv := newTestServer(func(context.Context, []byte) (*evaluationResponse, error) { return nil, err }, 1, 1)
		res, out := post(t, srv.URL, validBody, sign(validBody))
		require.Equal(t, status, res.StatusCode, err.Error())
		require.NotEmpty(t, out["error"])
		srv.Close()
	}
}

func TestBusyReturns503(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 1)
	srv := newTestServer(func(ctx context.Context, _ []byte) (*evaluationResponse, error) {
		started <- struct{}{}
		<-release
		return okEval(ctx, nil)
	}, 1, 0)
	defer srv.Close()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		res, _ := post(t, srv.URL, validBody, sign(validBody))
		assert.Equal(t, http.StatusOK, res.StatusCode)
	}()
	<-started

	res, _ := post(t, srv.URL, validBody, sign(validBody))
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
	require.Equal(t, "10", res.Header.Get("Retry-After"))

	close(release)
	wg.Wait()
}

func TestQueueRunsInParallelUpToLimit(t *testing.T) {
	var mu sync.Mutex
	running, peak := 0, 0
	srv := newTestServer(func(ctx context.Context, _ []byte) (*evaluationResponse, error) {
		mu.Lock()
		running++
		peak = max(peak, running)
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
		mu.Lock()
		running--
		mu.Unlock()
		return okEval(ctx, nil)
	}, 2, 10)
	defer srv.Close()

	var wg sync.WaitGroup
	for range 6 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := post(t, srv.URL, validBody, sign(validBody))
			assert.Equal(t, http.StatusOK, res.StatusCode)
		}()
	}
	wg.Wait()
	require.Equal(t, 2, peak)
}

// fakeCLI writes a script standing in for the cre binary.
func fakeCLI(t *testing.T, output string) string {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	require.NoError(t, os.WriteFile(out, []byte(output), 0o600))
	script := filepath.Join(dir, "cre")
	body := `#!/bin/sh
printf '%s\n' "$@" > "` + dir + `/args"
env > "` + dir + `/env"
cat "` + out + `"
`
	require.NoError(t, os.WriteFile(script, []byte(body), 0o700))
	return script
}

func TestCLISimulator(t *testing.T) {
	t.Setenv("RUNNER_SHARED_SECRET", "must-not-leak")
	bin := fakeCLI(t, fixture(t, "success.txt"))
	sim := &cliSimulator{
		Bin: bin, Dir: t.TempDir(), Workflow: "test-workflow", Target: "local-simulation",
		Wasm: "/app/build/workflow.wasm", Timeout: 10 * time.Second, Tokens: ghapp.Static("ghs_abc"),
	}

	res, err := sim.Evaluate(context.Background(), validBody)
	require.NoError(t, err)
	require.Equal(t, 30, res.Score)

	args, _ := os.ReadFile(filepath.Join(filepath.Dir(bin), "args"))
	require.Equal(t, strings.Join([]string{"workflow", "simulate", "test-workflow", "--target", "local-simulation",
		"--non-interactive", "--trigger-index", "0", "--http-payload", string(validBody),
		"--wasm", "/app/build/workflow.wasm"}, "\n")+"\n", string(args))

	env, _ := os.ReadFile(filepath.Join(filepath.Dir(bin), "env"))
	require.Contains(t, string(env), "GITHUB_TOKEN_VALUE=ghs_abc")
	require.NotContains(t, string(env), "must-not-leak")
}

func TestCLISimulatorFailureAndMissingBinary(t *testing.T) {
	sim := &cliSimulator{Bin: fakeCLI(t, fixture(t, "failure.txt")), Dir: t.TempDir(), Workflow: "w",
		Target: "t", Timeout: 10 * time.Second, Tokens: ghapp.Static("x")}
	_, err := sim.Evaluate(context.Background(), validBody)
	var ee *evalError
	require.ErrorAs(t, err, &ee)

	sim.Bin = "/nonexistent/cre"
	_, err = sim.Evaluate(context.Background(), validBody)
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "no result"), err.Error())
	require.NotErrorAs(t, err, &ee)
}

var mergedBody = []byte(`{"repository":"acme/pool","pr_number":7,"campaign_id":"c","event":"merged"}`)

func eligibleEval(calls *atomic.Int32) func(context.Context, []byte) (*evaluationResponse, error) {
	return func(context.Context, []byte) (*evaluationResponse, error) {
		n := calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return &evaluationResponse{Score: 90, Eligible: true, Reward: "450000000", EvaluationHash: fmt.Sprintf("0x%d", n)}, nil
	}
}

func TestMergedIsSettledOnce(t *testing.T) {
	var calls atomic.Int32
	srv := newTestServer(eligibleEval(&calls), 4, 4)
	defer srv.Close()

	// Concurrent duplicates (webhook retries) wait for the first and get its result.
	var wg sync.WaitGroup
	hashes := make(chan any, 5)
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, out := post(t, srv.URL, mergedBody, sign(mergedBody))
			assert.Equal(t, http.StatusOK, res.StatusCode)
			hashes <- out["evaluation_hash"]
		}()
	}
	wg.Wait()
	close(hashes)
	for h := range hashes {
		require.Equal(t, "0x1", h)
	}
	require.Equal(t, int32(1), calls.Load())

	res, _ := post(t, srv.URL, mergedBody, sign(mergedBody))
	require.Equal(t, "true", res.Header.Get("X-ContribOracle-Replay"))

	// "opened" is a preview and is never deduplicated.
	opened := []byte(`{"repository":"acme/pool","pr_number":7,"campaign_id":"c","event":"opened"}`)
	post(t, srv.URL, opened, sign(opened))
	require.Equal(t, int32(2), calls.Load())
}

func TestIneligibleMergedIsNotRecorded(t *testing.T) {
	var calls atomic.Int32
	srv := newTestServer(func(context.Context, []byte) (*evaluationResponse, error) {
		calls.Add(1)
		return &evaluationResponse{Score: 40, Reward: "0", EvaluationHash: "0xa"}, nil
	}, 1, 1)
	defer srv.Close()

	post(t, srv.URL, mergedBody, sign(mergedBody))
	res, _ := post(t, srv.URL, mergedBody, sign(mergedBody))
	require.Empty(t, res.Header.Get("X-ContribOracle-Replay"))
	require.Equal(t, int32(2), calls.Load()) // Can be re-evaluated, e.g. after a late approval.
}

func TestLedgerPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	l, err := openLedger(path)
	require.NoError(t, err)
	req, _ := parseRequest(mergedBody)
	e := l.acquire(settlementKey(req))
	require.NoError(t, l.record(settlementKey(req), e, &evaluationResponse{Score: 90, Eligible: true, Reward: "1", EvaluationHash: "0xabc"}))
	e.release()

	reopened, err := openLedger(path)
	require.NoError(t, err)
	e = reopened.acquire(settlementKey(req))
	defer e.release()
	require.Equal(t, "0xabc", e.res.EvaluationHash)
}

func TestWarmsReviewersBeforeSimulating(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]string{}
	status := http.StatusOK
	rev := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer rev-token", r.Header.Get("Authorization"))
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen[r.URL.Path] = string(b)
		mu.Unlock()
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error":"llm down"}`)
	}))
	defer rev.Close()

	var evals atomic.Int32
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	l, _ := openLedger("")
	s := newServer([]byte(secret), fakeEval{func(ctx context.Context, p []byte) (*evaluationResponse, error) {
		evals.Add(1)
		return okEval(ctx, p)
	}}, l, 1, 1, time.Second, log)
	s.warm = &reviewWarmer{baseURL: rev.URL, token: "rev-token", personas: []string{"code", "issue"}, http: http.DefaultClient}
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	sha := strings.Repeat("a", 40)
	body := []byte(`{"repository":"acme/pool","pr_number":1,"campaign_id":"c","event":"opened","head_sha":"` + sha + `"}`)
	res, _ := post(t, srv.URL, body, sign(body))
	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, int32(1), evals.Load())
	require.Len(t, seen, 2)
	require.Contains(t, seen["/review/code"], sha)
	require.Contains(t, seen["/review/issue"], `"pr_number":1`)

	// Reviewer failure: 502 with its message, and no simulation is run.
	status = http.StatusBadGateway
	res, out := post(t, srv.URL, body, sign(body))
	require.Equal(t, http.StatusBadGateway, res.StatusCode)
	require.Contains(t, out["error"], "llm down")
	require.Equal(t, int32(1), evals.Load())
}
