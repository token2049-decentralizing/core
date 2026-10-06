// reviewer scores pull requests with an LLM for the CRE workflow.
//
//	POST /review/code   correctness, tests, code quality, security
//	POST /review/issue  issue relevance, value, scope
//	GET  /healthz
//
// Callers authenticate with "Authorization: Bearer $REVIEWER_TOKEN".
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

func run(log *slog.Logger) error {
	token := os.Getenv("REVIEWER_TOKEN")
	if len(token) < 16 {
		return errors.New("REVIEWER_TOKEN must be at least 16 characters")
	}
	llm := &llmClient{
		baseURL:  env("LLM_BASE_URL", "https://ark.ap-southeast.bytepluses.com/api/v3"),
		apiKey:   os.Getenv("LLM_API_KEY"),
		model:    os.Getenv("LLM_MODEL"),
		jsonMode: os.Getenv("LLM_JSON_MODE") == "true",
		http:     &http.Client{},
	}
	if llm.apiKey == "" || llm.model == "" {
		return errors.New("LLM_API_KEY and LLM_MODEL are required")
	}
	if extra := os.Getenv("LLM_EXTRA_BODY"); extra != "" {
		if err := json.Unmarshal([]byte(extra), &llm.extraBody); err != nil {
			return fmt.Errorf("LLM_EXTRA_BODY must be a JSON object: %w", err)
		}
	}
	tokens, err := ghapp.SourceFromEnv()
	if err != nil {
		return err
	}
	timeout := time.Duration(envInt("LLM_TIMEOUT_SECONDS", 120)) * time.Second
	rev := &reviewer{
		gh:           newGitHub(os.Getenv("GITHUB_API_URL"), tokens),
		llm:          llm,
		maxDiffChars: envInt("MAX_DIFF_CHARS", 60000),
	}
	srv := newServer([]byte(token), rev, timeout, log)

	httpSrv := &http.Server{
		Addr:              ":" + env("REVIEWER_PORT", "8090"),
		Handler:           srv.routes(),
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      timeout + 30*time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	log.Info("reviewer listening", "addr", httpSrv.Addr, "llm", llm.baseURL, "model", llm.model)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
