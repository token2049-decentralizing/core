// runner exposes the CRE workflow over HTTP for the GitHub App.
//
//	POST /evaluate  (signed with X-ContribOracle-Signature)
//	GET  /healthz
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"contriboracle/internal/ghapp"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// tokens prefers the GitHub App; a static GITHUB_TOKEN_VALUE is the dev fallback.
func tokens() (tokenSource, error) {
	if os.Getenv("GITHUB_APP_ID") != "" {
		return ghapp.FromEnv()
	}
	if t := os.Getenv("GITHUB_TOKEN_VALUE"); t != "" {
		return staticToken(t), nil
	}
	return nil, errors.New("set GITHUB_APP_* vars (or GITHUB_TOKEN_VALUE for dev)")
}

// creAuthMode reports what the cre CLI will use. CRE_API_KEY wins over a login session.
func creAuthMode() string {
	if os.Getenv("CRE_API_KEY") != "" {
		return "api_key"
	}
	if home, err := os.UserHomeDir(); err == nil {
		if _, err := os.Stat(filepath.Join(home, ".cre", "cre.yaml")); err == nil {
			return "login_session"
		}
	}
	return "none"
}

func run(log *slog.Logger) error {
	secret := os.Getenv("RUNNER_SHARED_SECRET")
	if len(secret) < 16 {
		return errors.New("RUNNER_SHARED_SECRET must be at least 16 characters")
	}
	ts, err := tokens()
	if err != nil {
		return err
	}
	sim := &cliSimulator{
		Bin:      env("CRE_BIN", "cre"),
		Dir:      env("CRE_PROJECT_DIR", "."),
		Workflow: env("CRE_WORKFLOW", "test-workflow"),
		Target:   env("CRE_TARGET", "staging-settings"),
		Wasm:     os.Getenv("CRE_WASM"),
		Timeout:  time.Duration(envInt("EVAL_TIMEOUT_SECONDS", 120)) * time.Second,
		Tokens:   ts,
	}
	concurrency := envInt("MAX_CONCURRENCY", 2)
	if sim.Wasm == "" && concurrency > 1 {
		// Without a prebuilt WASM the CLI compiles to a fixed temp file; parallel runs would clash.
		log.Warn("CRE_WASM not set, limiting to 1 concurrent evaluation")
		concurrency = 1
	}
	queueWait := time.Duration(envInt("QUEUE_WAIT_SECONDS", 60)) * time.Second
	srv := newServer([]byte(secret), sim, concurrency, envInt("MAX_QUEUE", 16), queueWait, log)

	httpSrv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      queueWait + sim.Timeout + 10*time.Second, // Queueing + one evaluation.
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	log.Info("runner listening", "addr", httpSrv.Addr, "workflow", sim.Workflow, "target", sim.Target,
		"wasm", sim.Wasm, "cre_auth", creAuthMode())

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), sim.Timeout)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx) // Lets in-flight evaluations finish.
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
