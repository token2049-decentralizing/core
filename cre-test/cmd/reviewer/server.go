package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

type reviewRequest struct {
	Repository string `json:"repository"`
	PRNumber   int    `json:"pr_number"`
	HeadSHA    string `json:"head_sha"` // Optional; empty = current head (runner cache warm-up).
}

var (
	repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	shaRe  = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// call is one in-flight or finished review; concurrent callers share it.
type call struct {
	done chan struct{}
	res  *reviewResult
	err  error
}

type server struct {
	token   []byte
	rev     *reviewer
	timeout time.Duration
	log     *slog.Logger

	mu    sync.Mutex
	calls map[string]*call // persona|repo|pr|head -> result; failures are not kept
}

func newServer(token []byte, rev *reviewer, timeout time.Duration, log *slog.Logger) *server {
	return &server{token: token, rev: rev, timeout: timeout, log: log, calls: map[string]*call{}}
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("POST /review/{persona}", s.handleReview)
	return mux
}

func (s *server) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), s.token) == 1
}

func (s *server) handleReview(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	name := r.PathValue("persona")
	if _, ok := personas[name]; !ok {
		writeError(w, http.StatusNotFound, "unknown persona "+name)
		return
	}
	var req reviewRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	switch {
	case !repoRe.MatchString(req.Repository):
		writeError(w, http.StatusBadRequest, "repository must be 'owner/repo'")
		return
	case req.PRNumber <= 0:
		writeError(w, http.StatusBadRequest, "pr_number must be positive")
		return
	case req.HeadSHA != "" && !shaRe.MatchString(req.HeadSHA):
		writeError(w, http.StatusBadRequest, "head_sha must be a 40-char hex SHA")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.timeout)
	defer cancel()
	start := time.Now()
	res, cached, err := s.reviewOnce(ctx, name, req)
	log := s.log.With("persona", name, "repository", req.Repository, "pr", req.PRNumber, "cached", cached, "ms", time.Since(start).Milliseconds())

	switch {
	case err == nil:
		log.Info("reviewed", "score", res.Score)
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, errHeadMoved):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, errNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		log.Warn("review timed out")
		writeError(w, http.StatusGatewayTimeout, "review timed out")
	default:
		log.Warn("review failed", "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

// reviewOnce returns the cached review for this commit, or runs it once for all concurrent callers.
func (s *server) reviewOnce(ctx context.Context, name string, req reviewRequest) (*reviewResult, bool, error) {
	if req.HeadSHA != "" {
		if res, ok := s.cached(name, req, req.HeadSHA); ok {
			return res, true, nil
		}
	}
	in, err := s.rev.prepare(ctx, req.Repository, req.PRNumber, req.HeadSHA)
	if err != nil {
		return nil, false, err
	}
	key := cacheKey(name, req, in.PR.Head.SHA)

	s.mu.Lock()
	c, running := s.calls[key]
	if !running {
		c = &call{done: make(chan struct{})}
		s.calls[key] = c
	}
	s.mu.Unlock()

	if running {
		select {
		case <-c.done:
			return c.res, true, c.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	// Detached from this caller so a disconnect does not waste a half-done LLM call.
	runCtx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	c.res, c.err = s.rev.review(runCtx, name, in)
	if c.err != nil {
		s.mu.Lock()
		delete(s.calls, key) // Let the next caller retry.
		s.mu.Unlock()
	}
	close(c.done)
	return c.res, false, c.err
}

func (s *server) cached(name string, req reviewRequest, head string) (*reviewResult, bool) {
	s.mu.Lock()
	c, ok := s.calls[cacheKey(name, req, head)]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	select {
	case <-c.done:
		return c.res, c.err == nil
	default:
		return nil, false
	}
}

func cacheKey(name string, req reviewRequest, head string) string {
	return fmt.Sprintf("%s|%s|%d|%s", name, req.Repository, req.PRNumber, head)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
