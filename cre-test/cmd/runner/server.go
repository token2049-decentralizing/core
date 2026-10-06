package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	signatureHeader = "X-ContribOracle-Signature"
	maxBodyBytes    = 64 << 10
)

type server struct {
	secret    []byte
	eval      evaluator
	admit     chan struct{} // running + queued; full -> 503
	workers   chan struct{} // running simulations
	queueWait time.Duration // max time queued before 503
	log       *slog.Logger
}

func newServer(secret []byte, eval evaluator, concurrency, queue int, queueWait time.Duration, log *slog.Logger) *server {
	return &server{
		secret:    secret,
		eval:      eval,
		admit:     make(chan struct{}, concurrency+queue),
		workers:   make(chan struct{}, concurrency),
		queueWait: queueWait,
		log:       log,
	}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("POST /evaluate", s.handleEvaluate)
	return mux
}

// validSignature checks "sha256=<hex HMAC-SHA256(body)>", same scheme as GitHub webhooks.
func validSignature(secret, body []byte, header string) bool {
	got, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return false
	}
	sig, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(sig, mac.Sum(nil))
}

func (s *server) handleEvaluate(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	if !validSignature(s.secret, body, r.Header.Get(signatureHeader)) {
		writeError(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	req, err := parseRequest(body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	select {
	case s.admit <- struct{}{}:
		defer func() { <-s.admit }()
	default:
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "busy, retry later")
		return
	}
	wait := time.NewTimer(s.queueWait)
	defer wait.Stop()
	select {
	case s.workers <- struct{}{}:
		defer func() { <-s.workers }()
	case <-wait.C:
		w.Header().Set("Retry-After", "10")
		writeError(w, http.StatusServiceUnavailable, "busy, retry later")
		return
	case <-r.Context().Done():
		return // Caller gave up while queued.
	}

	payload, _ := json.Marshal(req) // Re-encoded: only validated fields reach the CLI.
	start := time.Now()
	res, err := s.eval.Evaluate(r.Context(), payload)
	log := s.log.With("repository", req.Repository, "pr", req.PRNumber, "event", req.Event, "ms", time.Since(start).Milliseconds())

	var evalErr *evalError
	switch {
	case err == nil:
		log.Info("evaluated", "score", res.Score, "eligible", res.Eligible)
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, context.DeadlineExceeded):
		log.Warn("evaluation timed out")
		writeError(w, http.StatusGatewayTimeout, "evaluation timed out")
	case errors.As(err, &evalErr):
		log.Warn("evaluation failed", "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		log.Error("runner error", "error", err)
		writeError(w, http.StatusInternalServerError, "runner error")
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
