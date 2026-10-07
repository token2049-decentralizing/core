package reviewer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// llmClient talks to any OpenAI-compatible /chat/completions API
// (BytePlus ModelArk: https://ark.ap-southeast.bytepluses.com/api/v3).
type llmClient struct {
	baseURL   string
	apiKey    string
	model     string         // Model ID or ModelArk endpoint ID (ep-...).
	extraBody map[string]any // Provider-specific fields, e.g. {"thinking":{"type":"disabled"}}.
	jsonMode  bool           // Send response_format=json_object (only if the model supports it).
	http      *http.Client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// complete returns the assistant message content.
func (c *llmClient) complete(ctx context.Context, messages []chatMessage) (string, error) {
	body := map[string]any{}
	for k, v := range c.extraBody {
		body[k] = v
	}
	body["model"] = c.model
	body["messages"] = messages
	body["temperature"] = 0
	if c.jsonMode {
		body["response_format"] = map[string]string{"type": "json_object"}
	}
	raw, _ := json.Marshal(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.baseURL, "/")+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	res, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("llm: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return "", fmt.Errorf("llm -> HTTP %d: %s", res.StatusCode, truncate(string(b), 300))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("llm: bad response: %w", err)
	}
	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New("llm: empty response")
	}
	return out.Choices[0].Message.Content, nil
}

// extractJSON pulls the JSON object out of a reply that may have fences or prose around it.
func extractJSON(s string) (string, error) {
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return "", errors.New("no JSON object in model reply")
	}
	return s[start : end+1], nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
