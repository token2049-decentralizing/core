// Package ghapp mints short-lived GitHub App installation tokens, read-only unless asked otherwise.
package ghapp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// ReadOnly is what the workflow reads. Tokens are downscoped even if the App has more.
var ReadOnly = map[string]string{"pull_requests": "read", "contents": "read", "checks": "read", "metadata": "read"}

// Refresh this long before GitHub's expiry.
const refreshMargin = 5 * time.Minute

type Minter struct {
	APIURL         string // Default https://api.github.com
	AppID          string
	Key            *rsa.PrivateKey
	InstallationID string            // Optional if Repo is set.
	Repo           string            // "owner/repo", used to look up the installation.
	Permissions    map[string]string // Token scope; nil = ReadOnly.
	HTTP           *http.Client
	Now            func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// FromEnv reads GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY(_PATH) and
// GITHUB_APP_INSTALLATION_ID or GITHUB_APP_REPO.
func FromEnv() (*Minter, error) {
	m, err := appFromEnv()
	if err != nil {
		return nil, err
	}
	if m.InstallationID == "" && m.Repo == "" {
		return nil, errors.New("GITHUB_APP_INSTALLATION_ID or GITHUB_APP_REPO is required")
	}
	return m, nil
}

// appFromEnv reads the key from GITHUB_APP_PRIVATE_KEY (PEM contents, e.g. a Fly secret)
// or the file at GITHUB_APP_PRIVATE_KEY_PATH.
func appFromEnv() (*Minter, error) {
	appID := os.Getenv("GITHUB_APP_ID")
	pemBytes := []byte(os.Getenv("GITHUB_APP_PRIVATE_KEY"))
	if keyPath := os.Getenv("GITHUB_APP_PRIVATE_KEY_PATH"); len(pemBytes) == 0 && keyPath != "" {
		var err error
		if pemBytes, err = os.ReadFile(keyPath); err != nil {
			return nil, err
		}
	}
	if appID == "" || len(pemBytes) == 0 {
		return nil, errors.New("GITHUB_APP_ID and GITHUB_APP_PRIVATE_KEY or GITHUB_APP_PRIVATE_KEY_PATH are required")
	}
	key, err := ParseKey(pemBytes)
	if err != nil {
		return nil, err
	}
	return &Minter{
		APIURL:         os.Getenv("GITHUB_API_URL"),
		AppID:          appID,
		Key:            key,
		InstallationID: os.Getenv("GITHUB_APP_INSTALLATION_ID"),
		Repo:           os.Getenv("GITHUB_APP_REPO"),
	}, nil
}

// ParseKey accepts PKCS#1 (GitHub's format) or PKCS#8 RSA keys.
func ParseKey(pemBytes []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("private key is not PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return rk, nil
}

// AppJWT builds an RS256 App JWT, valid 9 min. iat is backdated 60s for clock drift.
func AppJWT(appID string, key *rsa.PrivateKey, now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	claims, _ := json.Marshal(map[string]any{"iat": now.Unix() - 60, "exp": now.Unix() + 540, "iss": appID})
	signingInput := header + "." + enc.EncodeToString(claims)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + enc.EncodeToString(sig), nil
}

// Token returns a cached installation token, minting a new one when close to expiry.
func (m *Minter) Token(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if m.token != "" && now.Before(m.expires.Add(-refreshMargin)) {
		return m.token, nil
	}
	jwt, err := AppJWT(m.AppID, m.Key, now)
	if err != nil {
		return "", err
	}

	if m.InstallationID == "" {
		var inst struct {
			ID int64 `json:"id"`
		}
		if err := m.call(ctx, http.MethodGet, "/repos/"+m.Repo+"/installation", jwt, nil, &inst); err != nil {
			return "", err
		}
		m.InstallationID = strconv.FormatInt(inst.ID, 10)
	}

	var res struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	path := "/app/installations/" + m.InstallationID + "/access_tokens"
	perms := m.Permissions
	if perms == nil {
		perms = ReadOnly
	}
	if err := m.call(ctx, http.MethodPost, path, jwt, map[string]any{"permissions": perms}, &res); err != nil {
		return "", err
	}
	if res.Token == "" {
		return "", errors.New("GitHub returned no token")
	}
	m.token, m.expires = res.Token, res.ExpiresAt
	return m.token, nil
}

func (m *Minter) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Minter) call(ctx context.Context, method, path, jwt string, body, out any) error {
	api := m.APIURL
	if api == "" {
		api = "https://api.github.com"
	}
	var reader io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, api+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "contriboracle")

	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("GitHub %s -> HTTP %d: %s", path, res.StatusCode, raw)
	}
	return json.Unmarshal(raw, out)
}

// TokenSource yields a GitHub token for API calls.
type TokenSource interface {
	Token(ctx context.Context) (string, error)
}

// Tokens yields the TokenSource for a repository: each repo can sit in a different App installation.
type Tokens interface {
	ForRepo(repo string) TokenSource
}

// Static is a fixed token (dev fallback: a personal fine-grained token).
type Static string

func (s Static) Token(context.Context) (string, error) { return string(s), nil }

func (s Static) ForRepo(string) TokenSource { return s }

// Pool mints tokens from whichever installation of the App covers each repository.
type Pool struct {
	base *Minter // AppID, Key, APIURL; a set InstallationID is used for every repo.

	mu      sync.Mutex
	minters map[string]*Minter
}

func NewPool(base *Minter) *Pool {
	return &Pool{base: base, minters: map[string]*Minter{}}
}

func (p *Pool) ForRepo(repo string) TokenSource {
	if p.base.InstallationID != "" {
		return p.base
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.minters[repo]
	if !ok {
		m = &Minter{APIURL: p.base.APIURL, AppID: p.base.AppID, Key: p.base.Key, Repo: repo, HTTP: p.base.HTTP,
			Now: p.base.Now, Permissions: p.base.Permissions}
		p.minters[repo] = m
	}
	return m
}

// WithPermissions returns tokens scoped to perms. A Static dev token has fixed scopes and is returned as is.
func WithPermissions(t Tokens, perms map[string]string) Tokens {
	p, ok := t.(*Pool)
	if !ok {
		return t
	}
	b := p.base
	return NewPool(&Minter{APIURL: b.APIURL, AppID: b.AppID, Key: b.Key, InstallationID: b.InstallationID,
		Repo: b.Repo, HTTP: b.HTTP, Now: b.Now, Permissions: perms})
}

// SourceFromEnv prefers the GitHub App; GITHUB_TOKEN_VALUE is the dev fallback.
// Without GITHUB_APP_INSTALLATION_ID the installation is looked up per repository.
func SourceFromEnv() (Tokens, error) {
	if os.Getenv("GITHUB_APP_ID") != "" {
		m, err := appFromEnv()
		if err != nil {
			return nil, err
		}
		return NewPool(m), nil
	}
	if t := os.Getenv("GITHUB_TOKEN_VALUE"); t != "" {
		return Static(t), nil
	}
	return nil, errors.New("set GITHUB_APP_* vars (or GITHUB_TOKEN_VALUE for dev)")
}

// Fallback uses primary's tokens and falls back to fallback for repos where primary can't
// mint one, e.g. primary asks for a permission the App or installation doesn't grant.
// A repo's primary failure is remembered for a while so each call doesn't retry it first.
func Fallback(primary, fallback Tokens) Tokens {
	return &fallbackTokens{primary: primary, fallback: fallback, failed: map[string]time.Time{}}
}

const fallbackRetry = 10 * time.Minute

type fallbackTokens struct {
	primary, fallback Tokens

	mu     sync.Mutex
	failed map[string]time.Time // repo -> when primary last failed
}

func (f *fallbackTokens) ForRepo(repo string) TokenSource {
	return fallbackSource{f: f, repo: repo}
}

type fallbackSource struct {
	f    *fallbackTokens
	repo string
}

func (s fallbackSource) Token(ctx context.Context) (string, error) {
	f := s.f
	f.mu.Lock()
	failedAt, failed := f.failed[s.repo]
	f.mu.Unlock()
	if !failed || time.Since(failedAt) > fallbackRetry {
		t, err := f.primary.ForRepo(s.repo).Token(ctx)
		if err == nil {
			return t, nil
		}
		f.mu.Lock()
		f.failed[s.repo] = time.Now()
		f.mu.Unlock()
	}
	return f.fallback.ForRepo(s.repo).Token(ctx)
}
