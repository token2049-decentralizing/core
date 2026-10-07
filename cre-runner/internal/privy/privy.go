// Package privy finds or pregenerates the Privy Solana wallet of a GitHub user, so rewards
// can be sent to contributors who have never signed in. When they later sign in with GitHub,
// Privy matches the same GitHub account and they control that wallet.
//
// REST API (https://api.privy.io): Basic auth app_id:app_secret plus the privy-app-id header.
package privy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	AppID     string
	AppSecret string
	BaseURL   string // Default https://api.privy.io
	HTTP      *http.Client
}

// Wallet is a GitHub user's Privy Solana embedded wallet.
type Wallet struct {
	PrivyUserID string
	Address     string
	// Pregenerated is true when this call created the Privy user (the person never signed in).
	Pregenerated bool
}

// ErrNotFound is returned by the API for unknown users.
var ErrNotFound = errors.New("privy: not found")

type linkedAccount struct {
	Type          string `json:"type"`
	Subject       string `json:"subject,omitempty"`
	Username      string `json:"username,omitempty"`
	Address       string `json:"address,omitempty"`
	ChainType     string `json:"chain_type,omitempty"`
	ConnectorType string `json:"connector_type,omitempty"`
	WalletClient  string `json:"wallet_client_type,omitempty"`
}

type user struct {
	ID             string          `json:"id"`
	LinkedAccounts []linkedAccount `json:"linked_accounts"`
}

var solanaWallet = []map[string]string{{"chain_type": "solana"}}

// WalletForGitHub returns the Solana wallet of the Privy user linked to this GitHub account,
// creating the user and/or the wallet if needed. githubID is the numeric GitHub user id,
// which Privy stores as the account subject; the login can change, the id cannot.
func (c *Client) WalletForGitHub(ctx context.Context, githubID int64, login string) (*Wallet, error) {
	if githubID <= 0 || login == "" {
		return nil, errors.New("privy: GitHub id and login are required")
	}
	subject := strconv.FormatInt(githubID, 10)

	u, err := c.userByGitHub(ctx, login)
	pregenerated := false
	switch {
	case errors.Is(err, ErrNotFound):
		u, err = c.createGitHubUser(ctx, subject, login)
		if err != nil {
			// A concurrent settlement may have created it first.
			if u2, lookupErr := c.userByGitHub(ctx, login); lookupErr == nil {
				u, err = u2, nil
			}
		} else {
			pregenerated = true
		}
		if err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	}

	// Logins can be renamed and reused: only trust the user if it is the same GitHub account.
	if s := githubSubject(u); s != subject {
		return nil, fmt.Errorf("privy: GitHub login %q belongs to another account in Privy (subject %s, want %s)", login, s, subject)
	}
	if addr := embeddedSolanaAddress(u); addr != "" {
		return &Wallet{PrivyUserID: u.ID, Address: addr, Pregenerated: pregenerated}, nil
	}
	// Signed in before Solana wallets were enabled: add one.
	if err := c.post(ctx, "/v1/users/"+u.ID+"/wallets", map[string]any{"wallets": solanaWallet}, u); err != nil {
		return nil, err
	}
	if addr := embeddedSolanaAddress(u); addr != "" {
		return &Wallet{PrivyUserID: u.ID, Address: addr, Pregenerated: pregenerated}, nil
	}
	return nil, fmt.Errorf("privy: user %s has no Solana embedded wallet", u.ID)
}

func (c *Client) userByGitHub(ctx context.Context, login string) (*user, error) {
	var u user
	if err := c.post(ctx, "/v1/users/github/username", map[string]string{"username": login}, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func (c *Client) createGitHubUser(ctx context.Context, subject, login string) (*user, error) {
	body := map[string]any{
		"linked_accounts": []linkedAccount{{Type: "github_oauth", Subject: subject, Username: login}},
		"wallets":         solanaWallet,
	}
	var u user
	if err := c.post(ctx, "/v1/users", body, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

func githubSubject(u *user) string {
	for _, a := range u.LinkedAccounts {
		if a.Type == "github_oauth" {
			return a.Subject
		}
	}
	return ""
}

func embeddedSolanaAddress(u *user) string {
	for _, a := range u.LinkedAccounts {
		if a.Type == "wallet" && a.ChainType == "solana" && (a.ConnectorType == "embedded" || a.WalletClient == "privy") {
			return a.Address
		}
	}
	return ""
}

func (c *Client) post(ctx context.Context, path string, body, out any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.privy.io"
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("privy-app-id", c.AppID)
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(c.AppID+":"+c.AppSecret)))

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("privy %s: %w", path, err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	switch {
	case res.StatusCode == http.StatusNotFound:
		return ErrNotFound
	case res.StatusCode < 200 || res.StatusCode > 299:
		msg := string(b)
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("privy %s -> HTTP %d: %s", path, res.StatusCode, msg)
	}
	return json.Unmarshal(b, out)
}
