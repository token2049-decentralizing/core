package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// evaluatorClient calls the CRE runner's POST /evaluate (cre-test/cmd/runner).
type evaluatorClient struct {
	url         string // runner base URL
	secret      []byte // RUNNER_SHARED_SECRET on the runner side
	http        *http.Client
	maxAttempts int
	sleep       func(ctx context.Context, d time.Duration) error
}

type evaluateRequest struct {
	Repository string `json:"repository"`
	PRNumber   int    `json:"pr_number"`
	CampaignID string `json:"campaign_id"`
	Event      string `json:"event"`
	HeadSHA    string `json:"head_sha,omitempty"`
}

type evaluateResult struct {
	Score          int    `json:"score"`
	Eligible       bool   `json:"eligible"`
	Reward         string `json:"reward"`
	EvaluationHash string `json:"evaluation_hash"`
	PolicyHash     string `json:"policy_hash"`
	Replayed       bool   `json:"-"`
}

func newEvaluatorClient(url, secret string) *evaluatorClient {
	return &evaluatorClient{
		url:    strings.TrimRight(url, "/"),
		secret: []byte(secret),
		// Runner worst case: queue wait + LLM review + simulation.
		http:        &http.Client{Timeout: 6 * time.Minute},
		maxAttempts: 4,
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func (c *evaluatorClient) sign(body []byte) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// Evaluate retries only when the runner is busy (503); other failures are final.
func (c *evaluatorClient) Evaluate(ctx context.Context, req evaluateRequest) (*evaluateResult, error) {
	body, _ := json.Marshal(req)
	for attempt := 1; ; attempt++ {
		res, retryAfter, err := c.post(ctx, body)
		if err == nil || retryAfter == 0 || attempt >= c.maxAttempts {
			return res, err
		}
		if err := c.sleep(ctx, retryAfter); err != nil {
			return nil, err
		}
	}
}

func (c *evaluatorClient) post(ctx context.Context, body []byte) (*evaluateResult, time.Duration, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url+"/evaluate", bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-ContribOracle-Signature", c.sign(body))
	res, err := c.http.Do(r)
	if err != nil {
		return nil, 0, fmt.Errorf("runner: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))

	if res.StatusCode == http.StatusOK {
		var out evaluateResult
		if err := json.Unmarshal(raw, &out); err != nil || out.EvaluationHash == "" {
			return nil, 0, errors.New("runner: unexpected response")
		}
		out.Replayed = res.Header.Get("X-ContribOracle-Replay") == "true"
		return &out, 0, nil
	}
	var e struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	err = fmt.Errorf("runner -> HTTP %d: %s", res.StatusCode, e.Error)
	if res.StatusCode == http.StatusServiceUnavailable {
		wait := 10 * time.Second
		if s, perr := strconv.Atoi(res.Header.Get("Retry-After")); perr == nil && s > 0 {
			wait = time.Duration(min(s, 60)) * time.Second
		}
		return nil, wait, err
	}
	return nil, 0, err
}
