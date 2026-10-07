package solana

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gagliardetto/solana-go/rpc/jsonrpc"
	"github.com/stretchr/testify/require"
)

const campaignUUID = "11111111-2222-3333-4444-555555555555"

// fakeRPC is an in-process Solana JSON-RPC node with a fixed set of accounts.
type fakeRPC struct {
	t        *testing.T
	mu       sync.Mutex
	accounts map[string]fakeAccount // base58 -> account
	sent     []*solanago.Transaction
}

type fakeAccount struct {
	owner solanago.PublicKey
	data  []byte
}

func (f *fakeRPC) RoundTrip(r *http.Request) (*http.Response, error) {
	w := httptest.NewRecorder()
	f.serve(w, r)
	return w.Result(), nil
}

func (f *fakeRPC) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var req struct {
		ID     json.RawMessage   `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	body, _ := io.ReadAll(r.Body)
	require.NoError(f.t, json.Unmarshal(body, &req))
	ctx := map[string]any{"slot": 1}
	var result any
	switch req.Method {
	case "getAccountInfo":
		var key string
		_ = json.Unmarshal(req.Params[0], &key)
		acc, ok := f.accounts[key]
		if !ok {
			result = map[string]any{"context": ctx, "value": nil}
			break
		}
		result = map[string]any{"context": ctx, "value": map[string]any{
			"data": []string{base64.StdEncoding.EncodeToString(acc.data), "base64"}, "executable": false,
			"lamports": 1_000_000, "owner": acc.owner.String(), "rentEpoch": 0, "space": len(acc.data),
		}}
	case "getLatestBlockhash":
		result = map[string]any{"context": ctx, "value": map[string]any{
			"blockhash": solanago.Hash{9}.String(), "lastValidBlockHeight": 100}}
	case "sendTransaction":
		var raw string
		_ = json.Unmarshal(req.Params[0], &raw)
		b, err := base64.StdEncoding.DecodeString(raw)
		require.NoError(f.t, err)
		tx, err := solanago.TransactionFromBytes(b)
		require.NoError(f.t, err)
		f.sent = append(f.sent, tx)
		result = tx.Signatures[0].String()
	case "getSignatureStatuses":
		result = map[string]any{"context": ctx, "value": []any{
			map[string]any{"slot": 1, "confirmations": nil, "err": nil, "confirmationStatus": "confirmed"}}}
	default:
		http.Error(w, "unexpected "+req.Method, http.StatusTeapot)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
}

func campaignData(mint solanago.PublicKey, status byte) []byte {
	b := append([]byte{}, campaignDiscriminator...)
	id, _ := uuidBytes(campaignUUID)
	b = append(b, id[:]...)
	b = append(b, make([]byte, 32)...) // sponsor
	b = append(b, mint[:]...)
	b = append(b, make([]byte, 32)...) // vault
	b = append(b, make([]byte, 8)...)  // max_reward_per_pr
	b = append(b, make([]byte, 32)...) // policy_hash
	b = append(b, status)
	return append(b, make([]byte, 18)...) // total_paid, payout_count, bumps
}

type testEnv struct {
	payouts   *Payouts
	rpc       *fakeRPC
	mint      solanago.PublicKey
	recipient solanago.PublicKey
	campaign  solanago.PublicKey
}

func setup(t *testing.T) *testEnv {
	payer, err := solanago.NewRandomPrivateKey()
	require.NoError(t, err)
	f := &fakeRPC{t: t, accounts: map[string]fakeAccount{}}
	client := rpc.NewWithCustomRPCClient(jsonrpc.NewClientWithOpts("http://solana.test", &jsonrpc.RPCClientOpts{
		HTTPClient: &http.Client{Transport: f},
	}))
	p := &Payouts{
		RPC: client, Payer: payer, ChainSelector: DevnetChainSelector,
		ProgramID:        solanago.MustPublicKeyFromBase58("FSy2V61Tvm6bVHV4dGtoJS7T16eE7ZNjvGHEyT3aw6MA"),
		ForwarderProgram: solanago.MustPublicKeyFromBase58(MockForwarderProgram),
		ForwarderState:   solanago.MustPublicKeyFromBase58(MockForwarderState),
	}
	env := &testEnv{payouts: p, rpc: f, mint: solanago.NewWallet().PublicKey(), recipient: solanago.NewWallet().PublicKey()}
	id, _ := uuidBytes(campaignUUID)
	env.campaign, err = p.pda([]byte("campaign"), id[:])
	require.NoError(t, err)
	f.accounts[env.campaign.String()] = fakeAccount{owner: p.ProgramID, data: campaignData(env.mint, campaignStatusActive)}
	f.accounts[env.mint.String()] = fakeAccount{owner: solanago.TokenProgramID, data: make([]byte, 82)}
	return env
}

func TestPrepareCreatesRecipientTokenAccount(t *testing.T) {
	env := setup(t)
	s, err := env.payouts.Prepare(context.Background(), campaignUUID, env.recipient.String())
	require.NoError(t, err)
	require.Equal(t, &Settings{ChainSelector: DevnetChainSelector, ProgramID: env.payouts.ProgramID.String(),
		ForwarderProgram: MockForwarderProgram, ForwarderState: MockForwarderState,
		Mint: env.mint.String(), TokenProgram: solanago.TokenProgramID.String()}, s)

	require.Len(t, env.rpc.sent, 1)
	tx := env.rpc.sent[0]
	require.NoError(t, tx.VerifySignatures())
	require.Equal(t, env.payouts.Payer.PublicKey(), tx.Message.AccountKeys[0]) // Payer signs and pays.
	ix := tx.Message.Instructions[0]
	program, err := tx.Message.Program(ix.ProgramIDIndex)
	require.NoError(t, err)
	require.Equal(t, solanago.SPLAssociatedTokenAccountProgramID, program)
	require.Equal(t, []byte{1}, []byte(ix.Data)) // CreateIdempotent
	ata, _, _ := solanago.FindAssociatedTokenAddress(env.recipient, env.mint)
	require.Equal(t, ata, tx.Message.AccountKeys[ix.Accounts[1]])
}

func TestPrepareSkipsExistingTokenAccount(t *testing.T) {
	env := setup(t)
	ata, _, _ := solanago.FindAssociatedTokenAddress(env.recipient, env.mint)
	env.rpc.accounts[ata.String()] = fakeAccount{owner: solanago.TokenProgramID, data: make([]byte, 165)}
	_, err := env.payouts.Prepare(context.Background(), campaignUUID, env.recipient.String())
	require.NoError(t, err)
	require.Empty(t, env.rpc.sent)
}

func TestPrepareToken2022Mint(t *testing.T) {
	env := setup(t)
	env.rpc.accounts[env.mint.String()] = fakeAccount{owner: solanago.Token2022ProgramID, data: make([]byte, 82)}
	s, err := env.payouts.Prepare(context.Background(), campaignUUID, env.recipient.String())
	require.NoError(t, err)
	require.Equal(t, solanago.Token2022ProgramID.String(), s.TokenProgram)
}

func TestPrepareRejects(t *testing.T) {
	env := setup(t)
	delete(env.rpc.accounts, env.campaign.String())
	_, err := env.payouts.Prepare(context.Background(), campaignUUID, env.recipient.String())
	require.True(t, errors.Is(err, ErrCampaignNotOnChain), err)

	env = setup(t)
	env.rpc.accounts[env.campaign.String()] = fakeAccount{owner: env.payouts.ProgramID, data: campaignData(env.mint, 1)} // Paused
	_, err = env.payouts.Prepare(context.Background(), campaignUUID, env.recipient.String())
	require.ErrorContains(t, err, "not active")

	env = setup(t)
	env.rpc.accounts[env.campaign.String()] = fakeAccount{owner: env.payouts.ProgramID, data: make([]byte, 200)}
	_, err = env.payouts.Prepare(context.Background(), campaignUUID, env.recipient.String())
	require.ErrorContains(t, err, "not a contrib_oracle campaign")

	_, err = env.payouts.Prepare(context.Background(), "not-a-uuid", env.recipient.String())
	require.Error(t, err)
	_, err = env.payouts.Prepare(context.Background(), campaignUUID, "0xabc")
	require.Error(t, err)
	require.Empty(t, env.rpc.sent)
}

func TestParsePrivateKey(t *testing.T) {
	k, err := solanago.NewRandomPrivateKey()
	require.NoError(t, err)
	got, err := ParsePrivateKey(k.String())
	require.NoError(t, err)
	require.Equal(t, k, got)

	// solana-keygen file: JSON array of the 64 secret bytes.
	path := filepath.Join(t.TempDir(), "id.json")
	ints := make([]int, len(k))
	for i, b := range k {
		ints[i] = int(b)
	}
	raw, _ := json.Marshal(ints)
	require.NoError(t, os.WriteFile(path, raw, 0o600))
	got, err = ParsePrivateKey(path)
	require.NoError(t, err)
	require.Equal(t, k, got)

	_, err = ParsePrivateKey("not-a-key")
	require.Error(t, err)
}
