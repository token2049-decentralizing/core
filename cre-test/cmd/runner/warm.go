package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// reviewWarmer runs the LLM reviews before the simulation. The workflow's own reviewer
// calls then hit the reviewer cache and stay under the CRE HTTP timeout (10s).
type reviewWarmer struct {
	baseURL  string // e.g. http://reviewer:8090
	token    string
	personas []string
	http     *http.Client
}

func (w *reviewWarmer) Warm(ctx context.Context, req *evaluationRequest) error {
	body, _ := json.Marshal(map[string]any{"repository": req.Repository, "pr_number": req.PRNumber, "head_sha": req.HeadSHA})
	var wg sync.WaitGroup
	errs := make([]error, len(w.personas))
	for i, p := range w.personas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = w.warmOne(ctx, p, body)
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (w *reviewWarmer) warmOne(ctx context.Context, persona string, body []byte) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(w.baseURL, "/")+"/review/"+persona, bytes.NewReader(body))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+w.token)
	res, err := w.http.Do(r)
	if err != nil {
		return &evalError{msg: fmt.Sprintf("reviewer %s: %v", persona, err)}
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		raw, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		_ = json.Unmarshal(raw, &e)
		return &evalError{msg: fmt.Sprintf("reviewer %s -> HTTP %d: %s", persona, res.StatusCode, e.Error)}
	}
	return nil
}
