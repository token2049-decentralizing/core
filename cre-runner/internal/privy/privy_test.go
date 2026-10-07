package privy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeAPI is an in-process Privy API keyed by GitHub login.
type fakeAPI struct {
	t       *testing.T
	mu      sync.Mutex
	users   map[string]*user // login -> user
	calls   []string
	created int
	// createFails makes POST /v1/users fail once, after storing the user (lost race).
	createFails bool
}

func (f *fakeAPI) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w.Result(), nil
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Equal(f.t, "app-1", r.Header.Get("privy-app-id"))
	require.Equal(f.t, "Basic "+base64.StdEncoding.EncodeToString([]byte("app-1:secret")), r.Header.Get("Authorization"))
	f.calls = append(f.calls, r.URL.Path)
	var body map[string]json.RawMessage
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)

	switch {
	case r.URL.Path == "/v1/users/github/username":
		var login string
		_ = json.Unmarshal(body["username"], &login)
		u, ok := f.users[login]
		if !ok {
			http.Error(w, `{"error":"User not found"}`, http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(u)
	case r.URL.Path == "/v1/users":
		var accounts []linkedAccount
		_ = json.Unmarshal(body["linked_accounts"], &accounts)
		require.JSONEq(f.t, `[{"chain_type":"solana"}]`, string(body["wallets"]))
		gh := accounts[0]
		require.Equal(f.t, "github_oauth", gh.Type)
		u := &user{ID: "did:privy:new", LinkedAccounts: []linkedAccount{
			gh, {Type: "wallet", ChainType: "solana", ConnectorType: "embedded", WalletClient: "privy", Address: "NewSoLWaLLet1111111111111111111111"},
		}}
		f.users[gh.Username] = u
		f.created++
		if f.createFails {
			f.createFails = false
			http.Error(w, `{"error":"conflict"}`, http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(u)
	case r.URL.Path == "/v1/users/did:privy:old/wallets":
		u := f.users["old"]
		u.LinkedAccounts = append(u.LinkedAccounts, linkedAccount{Type: "wallet", ChainType: "solana", ConnectorType: "embedded", Address: "AddedSoLWaLLet111111111111111111"})
		_ = json.NewEncoder(w).Encode(u)
	default:
		http.Error(w, "unexpected "+r.URL.Path, http.StatusTeapot)
	}
}

func newTestClient(t *testing.T, users map[string]*user) (*Client, *fakeAPI) {
	f := &fakeAPI{t: t, users: users}
	return &Client{AppID: "app-1", AppSecret: "secret", HTTP: &http.Client{Transport: f}}, f
}

func github(subject, login string) linkedAccount {
	return linkedAccount{Type: "github_oauth", Subject: subject, Username: login}
}

func TestExistingUserWallet(t *testing.T) {
	c, f := newTestClient(t, map[string]*user{"alice": {ID: "did:privy:alice", LinkedAccounts: []linkedAccount{
		github("101", "alice"),
		{Type: "wallet", ChainType: "ethereum", ConnectorType: "embedded", Address: "0xabc"},
		{Type: "wallet", ChainType: "solana", ConnectorType: "injected", Address: "PhantomWaLLet"}, // External wallet: not ours to pay.
		{Type: "wallet", ChainType: "solana", ConnectorType: "embedded", WalletClient: "privy", Address: "AliceSoL"},
	}}})
	w, err := c.WalletForGitHub(context.Background(), 101, "alice")
	require.NoError(t, err)
	require.Equal(t, &Wallet{PrivyUserID: "did:privy:alice", Address: "AliceSoL"}, w)
	require.Zero(t, f.created)
}

func TestPregeneratesWalletForNewGitHubUser(t *testing.T) {
	c, f := newTestClient(t, map[string]*user{})
	w, err := c.WalletForGitHub(context.Background(), 202, "bob")
	require.NoError(t, err)
	require.True(t, w.Pregenerated)
	require.Equal(t, "NewSoLWaLLet1111111111111111111111", w.Address)
	require.Equal(t, github("202", "bob"), f.users["bob"].LinkedAccounts[0]) // Subject is the numeric GitHub id.

	// Next time the same user is found, nothing is created.
	w, err = c.WalletForGitHub(context.Background(), 202, "bob")
	require.NoError(t, err)
	require.False(t, w.Pregenerated)
	require.Equal(t, 1, f.created)
}

func TestLostCreateRaceFallsBackToLookup(t *testing.T) {
	c, f := newTestClient(t, map[string]*user{})
	f.createFails = true
	w, err := c.WalletForGitHub(context.Background(), 202, "bob")
	require.NoError(t, err)
	require.Equal(t, "NewSoLWaLLet1111111111111111111111", w.Address)
}

func TestRenamedLoginOwnedByAnotherAccount(t *testing.T) {
	c, _ := newTestClient(t, map[string]*user{"carol": {ID: "did:privy:x", LinkedAccounts: []linkedAccount{github("999", "carol")}}})
	_, err := c.WalletForGitHub(context.Background(), 303, "carol")
	require.ErrorContains(t, err, "belongs to another account")
}

func TestAddsSolanaWalletToExistingUser(t *testing.T) {
	c, f := newTestClient(t, map[string]*user{"old": {ID: "did:privy:old", LinkedAccounts: []linkedAccount{github("404", "old")}}})
	w, err := c.WalletForGitHub(context.Background(), 404, "old")
	require.NoError(t, err)
	require.Equal(t, "AddedSoLWaLLet111111111111111111", w.Address)
	require.Contains(t, f.calls, "/v1/users/did:privy:old/wallets")
}

func TestRequiresGitHubIdentity(t *testing.T) {
	c, _ := newTestClient(t, map[string]*user{})
	_, err := c.WalletForGitHub(context.Background(), 0, "x")
	require.Error(t, err)
}
