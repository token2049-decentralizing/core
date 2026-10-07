// Package reviewer scores pull requests with an LLM for the CRE workflow.
//
//	POST /review/code   correctness, tests, code quality, security
//	POST /review/issue  issue relevance, value, scope
//
// Callers authenticate with "Authorization: Bearer <token>".
package reviewer

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
)

type reviewRequest struct {
	Repository string `json:"repository"`
	PRNumber   int    `json:"pr_number"`
	HeadSHA    string `json:"head_sha"` // Optional; empty = current head (cache warm-up).
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

type Service struct {
	token   []byte
	rev     *reviewer
	timeout time.Duration
	log     *slog.Logger

	mu    sync.Mutex
	calls map[string]*call // persona|repo|pr|head -> result; failures are not kept
}

func newService(token []byte, rev *reviewer, timeout time.Duration, log *slog.Logger) *Service {
	return &Service{token: token, rev: rev, timeout: timeout, log: log, calls: map[string]*call{}}
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// FromEnv builds the reviewer from LLM_* env vars. It returns nil when neither
// LLM_API_KEY nor LLM_MODEL is set: the workflow then uses stub reviewer scores.
func FromEnv(token string, tokens ghapp.Tokens, log *slog.Logger) (*Service, error) {
	llm := &llmClient{
		baseURL:  os.Getenv("LLM_BASE_URL"),
		apiKey:   os.Getenv("LLM_API_KEY"),
		model:    os.Getenv("LLM_MODEL"),
		jsonMode: os.Getenv("LLM_JSON_MODE") == "true",
		http:     &http.Client{},
	}
	switch {
	case llm.apiKey == "" && llm.model == "":
		return nil, nil
	case llm.apiKey == "" || llm.model == "":
		return nil, errors.New("LLM_API_KEY and LLM_MODEL must both be set")
	case len(token) < 16:
		return nil, errors.New("reviewer token must be at least 16 characters")
	}
	if llm.baseURL == "" {
		llm.baseURL = "https://ark.ap-southeast.bytepluses.com/api/v3"
	}
	if extra := os.Getenv("LLM_EXTRA_BODY"); extra != "" {
		if err := json.Unmarshal([]byte(extra), &llm.extraBody); err != nil {
			return nil, fmt.Errorf("LLM_EXTRA_BODY must be a JSON object: %w", err)
		}
	}
	rev := &reviewer{
		gh:           newGitHub(os.Getenv("GITHUB_API_URL"), tokens),
		llm:          llm,
		maxDiffChars: envInt("MAX_DIFF_CHARS", 60000),
	}
	timeout := time.Duration(envInt("LLM_TIMEOUT_SECONDS", 120)) * time.Second
	log.Info("reviewer enabled", "llm", llm.baseURL, "model", llm.model)
	return newService([]byte(token), rev, timeout, log), nil
}

// Handler serves POST /review/{persona}.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /review/{persona}", s.handleReview)
	return mux
}

// Timeout bounds one review, including the LLM call.
func (s *Service) Timeout() time.Duration { return s.timeout }

// Warm runs every persona's review for the PR so the workflow's own reviewer calls hit
// the cache and stay under the CRE HTTP timeout (10s); an LLM review can take longer.
func (s *Service) Warm(ctx context.Context, repo string, pr int, headSHA string) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	req := reviewRequest{Repository: repo, PRNumber: pr, HeadSHA: headSHA}
	names := []string{"code", "issue"}
	errs := make([]error, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.reviewOnce(ctx, name, req); err != nil {
				errs[i] = fmt.Errorf("reviewer %s: %w", name, err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

func (s *Service) authorized(r *http.Request) bool {
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && subtle.ConstantTimeCompare([]byte(got), s.token) == 1
}

func (s *Service) handleReview(w http.ResponseWriter, r *http.Request) {
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
func (s *Service) reviewOnce(ctx context.Context, name string, req reviewRequest) (*reviewResult, bool, error) {
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

func (s *Service) cached(name string, req reviewRequest, head string) (*reviewResult, bool) {
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
