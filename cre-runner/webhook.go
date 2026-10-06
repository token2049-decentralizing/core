package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/supabase-community/supabase-go"
)

const webhookEventsTable = "github_webhook_events"

// webhookEvent maps to a row in public.github_webhook_events (see schema.sql).
type webhookEvent struct {
	DeliveryID         string            `json:"delivery_id"`
	Event              string            `json:"event"`
	Action             *string           `json:"action"`
	HookID             *int64            `json:"hook_id"`
	InstallationID     *int64            `json:"installation_id"`
	RepositoryFullName *string           `json:"repository_full_name"`
	SenderLogin        *string           `json:"sender_login"`
	SignatureValid     *bool             `json:"signature_valid"`
	Headers            map[string]string `json:"headers"`
	Payload            json.RawMessage   `json:"payload"`
	FromBot            bool              `json:"-"`
}

// payloadSummary holds the common top-level fields shared by most GitHub webhook payloads.
type payloadSummary struct {
	Action       *string `json:"action"`
	Installation *struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository *struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Sender *struct {
		Login string `json:"login"`
		Type  string `json:"type"` // "User", "Bot", "Organization", ...
	} `json:"sender"`
}

// extractPayload returns the JSON payload, handling both the application/json
// and application/x-www-form-urlencoded (payload=...) content types.
func extractPayload(contentType string, body []byte) (json.RawMessage, error) {
	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		form, err := url.ParseQuery(string(body))
		if err != nil {
			return nil, err
		}
		body = []byte(form.Get("payload"))
	}
	if !json.Valid(body) {
		return nil, errors.New("payload is not valid JSON")
	}
	return body, nil
}

// verifySignature checks X-Hub-Signature-256 against the raw request body.
func verifySignature(secret string, body []byte, signature string) bool {
	sig, ok := strings.CutPrefix(signature, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

func buildWebhookEvent(h http.Header, body []byte, secret string) (*webhookEvent, error) {
	payload, err := extractPayload(h.Get("Content-Type"), body)
	if err != nil {
		return nil, err
	}

	ev := &webhookEvent{
		DeliveryID: h.Get("X-GitHub-Delivery"),
		Event:      h.Get("X-GitHub-Event"),
		Headers:    map[string]string{},
		Payload:    payload,
	}
	if ev.DeliveryID == "" || ev.Event == "" {
		return nil, errors.New("missing X-GitHub-Delivery or X-GitHub-Event header")
	}

	if id, err := strconv.ParseInt(h.Get("X-GitHub-Hook-ID"), 10, 64); err == nil {
		ev.HookID = &id
	}
	if secret != "" {
		valid := verifySignature(secret, body, h.Get("X-Hub-Signature-256"))
		ev.SignatureValid = &valid
	}

	for k := range h {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-github-") || strings.HasPrefix(lk, "x-hub-") ||
			lk == "user-agent" || lk == "content-type" {
			ev.Headers[lk] = h.Get(k)
		}
	}

	var s payloadSummary
	if err := json.Unmarshal(payload, &s); err == nil {
		ev.Action = s.Action
		if s.Installation != nil {
			ev.InstallationID = &s.Installation.ID
		}
		if s.Repository != nil {
			ev.RepositoryFullName = &s.Repository.FullName
		}
		if s.Sender != nil {
			ev.SenderLogin = &s.Sender.Login
			ev.FromBot = s.Sender.Type == "Bot" || strings.HasSuffix(s.Sender.Login, "[bot]")
		}
	}

	return ev, nil
}

// saveWebhookEvent inserts one row per request; redeliveries share a delivery_id.
func saveWebhookEvent(client *supabase.Client, ev *webhookEvent) error {
	_, _, err := client.From(webhookEventsTable).
		Insert(ev, false, "", "minimal", "").
		Execute()
	return err
}
