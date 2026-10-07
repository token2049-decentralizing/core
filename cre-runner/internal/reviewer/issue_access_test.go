package reviewer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
)

type roundTrip func(*http.Request) *http.Response

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r), nil }

func respond(status int, body any) *http.Response {
	w := httptest.NewRecorder()
	w.WriteHeader(status)
	if s, ok := body.(string); ok {
		_, _ = io.WriteString(w, s)
	} else {
		_ = json.NewEncoder(w).Encode(body)
	}
	return w.Result()
}

// An issue the App may not read (403, no Issues permission) must not fail the review.
func TestUnreadableLinkedIssueStillReviews(t *testing.T) {
	gh := newGitHub("http://github.test", ghapp.Static("gh"))
	gh.http = &http.Client{Transport: roundTrip(func(r *http.Request) *http.Response {
		switch {
		case r.URL.Path == "/repos/acme/pool/pulls/7" && r.Header.Get("Accept") == "application/vnd.github.diff":
			return respond(200, "diff --git a/src/pool.go b/src/pool.go\n+func fixed() {}\n")
		case r.URL.Path == "/repos/acme/pool/pulls/7":
			return respond(200, map[string]any{"title": "Fix race", "body": "Fixes #21",
				"head": map[string]any{"sha": head}, "additions": 2, "deletions": 0})
		case r.URL.Path == "/repos/acme/pool/issues/21":
			return respond(403, `{"message":"Resource not accessible by integration"}`)
		}
		return respond(404, "{}")
	})}

	var mu sync.Mutex
	var prompts []string
	llm := &llmClient{baseURL: "http://llm.test/v1", apiKey: "k", model: "m", http: &http.Client{Transport: roundTrip(func(r *http.Request) *http.Response {
		var body struct {
			Messages []chatMessage `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		prompts = append(prompts, body.Messages[len(body.Messages)-1].Content)
		mu.Unlock()
		reply := `{"categories":{"correctness":30,"tests":10,"code_quality":20,"security":10,"issue_relevance":40,"value":20,"scope":15},"findings":[],"summary":"ok"}`
		return respond(200, map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	})}}

	s := newService([]byte(token), &reviewer{gh: gh, llm: llm, maxDiffChars: 10000}, 5*time.Second,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, s.Warm(context.Background(), "acme/pool", 7, head))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, prompts, 2) // code + issue personas
	for _, p := range prompts {
		require.Contains(t, p, "linked_issue_unreadable")
		require.Contains(t, p, "issue #21")
		require.False(t, strings.Contains(p, `"linked_issue":`), "no issue text to include")
	}
}
