package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	solanago "github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/supabase-community/supabase-go"
	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
	"github.com/token2049-decentralizing/core/cre-runner/internal/privy"
	"github.com/token2049-decentralizing/core/cre-runner/internal/reviewer"
	"github.com/token2049-decentralizing/core/cre-runner/internal/solana"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	n, err := strconv.Atoi(os.Getenv(key))
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// runnerInstance identifies this machine in cre_executions.runner_instance.
func runnerInstance() string {
	if id := os.Getenv("FLY_MACHINE_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	return host
}

// solanaPayoutsFromEnv enables on-chain payouts when CRE_SOLANA_PRIVATE_KEY (the key the
// cre CLI signs the forwarder transaction with) and SOLANA_PROGRAM_ID are set.
func solanaPayoutsFromEnv() (*solana.Payouts, error) {
	key, programID := os.Getenv("CRE_SOLANA_PRIVATE_KEY"), os.Getenv("SOLANA_PROGRAM_ID")
	if key == "" || programID == "" {
		return nil, nil
	}
	payer, err := solana.ParsePrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("CRE_SOLANA_PRIVATE_KEY: %w", err)
	}
	// `cre workflow simulate --broadcast` refuses the CLI's default EVM key even when the
	// workflow only writes to Solana; fail at startup instead of on every payout.
	if !validEVMKey(os.Getenv("CRE_ETH_PRIVATE_KEY")) {
		return nil, errors.New("CRE_ETH_PRIVATE_KEY must be a real 32-byte hex key for --broadcast " +
			"(any fresh, unfunded key: openssl rand -hex 32)")
	}
	keys := map[string]string{
		"SOLANA_PROGRAM_ID":        programID,
		"SOLANA_FORWARDER_PROGRAM": env("SOLANA_FORWARDER_PROGRAM", solana.MockForwarderProgram),
		"SOLANA_FORWARDER_STATE":   env("SOLANA_FORWARDER_STATE", solana.MockForwarderState),
	}
	parsed := map[string]solanago.PublicKey{}
	for name, v := range keys {
		if parsed[name], err = solanago.PublicKeyFromBase58(v); err != nil {
			return nil, fmt.Errorf("%s %q is not a Solana address", name, v)
		}
	}
	selector, err := strconv.ParseUint(env("SOLANA_CHAIN_SELECTOR", strconv.FormatUint(solana.DevnetChainSelector, 10)), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("SOLANA_CHAIN_SELECTOR: %w", err)
	}
	return &solana.Payouts{
		RPC:              rpc.New(env("SOLANA_RPC_URL", solana.DevnetRPC)),
		Payer:            payer,
		ChainSelector:    selector,
		ProgramID:        parsed["SOLANA_PROGRAM_ID"],
		ForwarderProgram: parsed["SOLANA_FORWARDER_PROGRAM"],
		ForwarderState:   parsed["SOLANA_FORWARDER_STATE"],
	}, nil
}

// validEVMKey: 64 hex characters (optional 0x), not zero and not the CLI's default key 0x…01.
func validEVMKey(k string) bool {
	k = strings.TrimPrefix(strings.TrimSpace(k), "0x")
	if len(k) != 64 {
		return false
	}
	b, err := hex.DecodeString(k)
	if err != nil {
		return false
	}
	nonZero := false
	for _, c := range b[:31] {
		nonZero = nonZero || c != 0
	}
	return nonZero || b[31] > 1
}

// setupExecutions mounts the reviewer and returns the CRE executor, or nil when no
// GitHub credentials are configured (webhooks are then only recorded).
func setupExecutions(r *gin.Engine, db *supabase.Client, port string) (*executor, error) {
	tokens, err := ghapp.SourceFromEnv()
	if err != nil {
		log.Printf("CRE executions disabled: %v", err)
		return nil, nil
	}

	// The reviewer runs in this process; the token only guards the workflow's calls back to it.
	reviewerToken := os.Getenv("REVIEWER_TOKEN")
	if reviewerToken == "" {
		b := make([]byte, 32)
		_, _ = rand.Read(b)
		reviewerToken = hex.EncodeToString(b)
	}
	rev, err := reviewer.FromEnv(reviewerToken, tokens, slog.Default())
	if err != nil {
		return nil, err
	}
	reviewerURL := ""
	if rev != nil {
		r.POST("/review/:persona", gin.WrapH(rev.Handler()))
		reviewerURL = strings.TrimRight(env("REVIEWER_BASE_URL", "http://127.0.0.1:"+port), "/")
	} else {
		log.Printf("LLM_API_KEY/LLM_MODEL not set; the workflow uses stub reviewer scores")
		reviewerToken = ""
	}

	sim := &cliSimulator{
		Bin:           env("CRE_BIN", "cre"),
		Dir:           env("CRE_PROJECT_DIR", "cre"),
		Workflow:      env("CRE_WORKFLOW", "test-workflow"),
		Target:        env("CRE_TARGET", "local-simulation"),
		Wasm:          os.Getenv("CRE_WASM"),
		Timeout:       time.Duration(envInt("EVAL_TIMEOUT_SECONDS", 120)) * time.Second,
		Tokens:        tokens,
		ReviewerToken: reviewerToken,
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	creAuth, err := installCRELogin(home)
	if err != nil {
		return nil, fmt.Errorf("cre login: %w", err)
	}
	if creAuth == "none" {
		log.Printf("no CRE credentials (CRE_API_KEY, CRE_LOGIN_YAML or ~/.cre/cre.yaml); executions will fail to authenticate")
	}
	concurrency := envInt("MAX_CONCURRENCY", 2)
	if sim.Wasm == "" && concurrency > 1 {
		// Without a prebuilt WASM the CLI compiles to a fixed temp file; parallel runs would clash.
		log.Printf("CRE_WASM not set, limiting to 1 concurrent execution")
		concurrency = 1
	}
	githubAPI := env("GITHUB_API_URL", "https://api.github.com")
	config := func(c campaignRow) (*workflowConfig, error) {
		return buildWorkflowConfig(c, githubAPI, reviewerURL)
	}
	x := newExecutor(supabaseStore{db}, sim, config, runnerInstance(), concurrency, envInt("MAX_QUEUE", 16), slog.Default())
	if rev != nil {
		x.warm = rev
	}
	// Merged PRs pay the author's Privy wallet, pregenerated from their GitHub account if needed.
	if appID, secret := os.Getenv("PRIVY_APP_ID"), os.Getenv("PRIVY_APP_SECRET"); appID != "" && secret != "" {
		x.wallets = &privy.Client{AppID: appID, AppSecret: secret, BaseURL: os.Getenv("PRIVY_API_URL")}
		log.Printf("payout wallets: Privy app %s", appID)
	} else {
		log.Printf("PRIVY_APP_ID/PRIVY_APP_SECRET not set; merged PRs are evaluated without a recipient wallet")
	}
	payouts, err := solanaPayoutsFromEnv()
	if err != nil {
		return nil, fmt.Errorf("solana payouts: %w", err)
	}
	if payouts != nil {
		if x.wallets == nil {
			return nil, errors.New("solana payouts need recipient wallets: set PRIVY_APP_ID and PRIVY_APP_SECRET")
		}
		x.payouts = payouts
		sim.Broadcast = true
		sim.SolanaRPC = env("SOLANA_RPC_URL", solana.DevnetRPC)
		log.Printf("solana payouts: program %s, payer %s, forwarder state %s",
			payouts.ProgramID, payouts.Payer.PublicKey(), payouts.ForwarderState)
	} else {
		log.Printf("CRE_SOLANA_PRIVATE_KEY/SOLANA_PROGRAM_ID not set; reward decisions are not paid on-chain")
	}
	if err := x.RecoverInterrupted(); err != nil {
		log.Printf("recover interrupted executions: %v", err)
	}
	log.Printf("CRE executions enabled: workflow=%s target=%s wasm=%q concurrency=%d instance=%s cre_auth=%s",
		sim.Workflow, sim.Target, sim.Wasm, concurrency, x.instance, creAuth)
	return x, nil
}

func main() {
	// Local development reads .env; in production (Fly) env vars come from secrets.
	// godotenv never overrides variables that are already set.
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("load .env: %v", err)
	}

	supabaseURL := os.Getenv("SUPABASE_URL")
	supabaseKey := os.Getenv("SUPABASE_SECRET_KEY")
	webhookSecret := os.Getenv("GITHUB_WEBHOOK_SECRET")

	client, err := supabase.NewClient(supabaseURL, supabaseKey, nil)
	if err != nil {
		log.Fatalf("supabase: %v", err)
	}
	if webhookSecret == "" {
		log.Printf("GITHUB_WEBHOOK_SECRET not set; webhook signatures will not be verified and no CRE executions will run")
	}

	corsOrigins := strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",")
	for i := range corsOrigins {
		corsOrigins[i] = strings.TrimSpace(corsOrigins[i])
	}
	if os.Getenv("CORS_ALLOWED_ORIGINS") == "" {
		corsOrigins = []string{"*"}
	}

	port := env("PORT", "8080")

	r := gin.Default()
	r.Use(corsMiddleware(corsOrigins))
	registerAPI(r, client)
	registerAppeals(r, newAppealsFromEnv(client))

	executions, err := setupExecutions(r, client, port)
	if err != nil {
		log.Fatalf("cre executions: %v", err)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"status":            "ok",
			"executions":        executions != nil,
			"webhook_signature": webhookSecret != "", // executions only start for signed deliveries
		})
	})

	r.POST("/webhook", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			log.Printf("webhook: failed to read body: %v", err)
			c.String(http.StatusBadRequest, "failed to read body")
			return
		}

		ev, err := buildWebhookEvent(c.Request.Header, body, webhookSecret)
		if err != nil {
			log.Printf("webhook: invalid request: %v", err)
			c.String(http.StatusBadRequest, err.Error())
			return
		}

		// Only user activity is recorded; bot-triggered events are acknowledged and dropped.
		if ev.FromBot {
			c.String(http.StatusOK, "ignored")
			return
		}

		// Record the delivery even if the signature is invalid, but reject it.
		if err := saveWebhookEvent(client, ev); err != nil {
			log.Printf("webhook: failed to save delivery %s: %v", ev.DeliveryID, err)
			c.String(http.StatusInternalServerError, "failed to save event")
			return
		}
		if ev.SignatureValid != nil && !*ev.SignatureValid {
			log.Printf("webhook: invalid signature for delivery %s", ev.DeliveryID)
			c.String(http.StatusUnauthorized, "invalid signature")
			return
		}

		log.Printf("webhook: saved delivery %s event=%s", ev.DeliveryID, ev.Event)
		// Executions run in the background and record their own status in cre_executions.
		// Unsigned deliveries never start one: they would let anyone spend LLM and CRE runs.
		switch t := prTriggerFrom(ev); {
		case executions == nil:
			// Startup logged why executions are disabled.
		case ev.SignatureValid == nil:
			log.Printf("webhook: delivery %s: no CRE execution, GITHUB_WEBHOOK_SECRET is not set", ev.DeliveryID)
		case t == nil:
			log.Printf("webhook: delivery %s: no CRE execution for event=%s action=%s", ev.DeliveryID, ev.Event, deref(ev.Action))
		default:
			executions.Submit(t)
		}
		c.String(http.StatusOK, "ack")
	})

	srv := &http.Server{Addr: ":" + port, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case err := <-errCh:
		log.Fatal(err)
	case <-ctx.Done():
	}

	log.Printf("shutting down")
	httpCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(httpCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("http shutdown: %v", err)
	}
	if executions != nil {
		// In-flight executions get SHUTDOWN_GRACE_SECONDS to finish; the rest are recorded as failed.
		grace := time.Duration(envInt("SHUTDOWN_GRACE_SECONDS", 240)) * time.Second
		execCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		executions.Shutdown(execCtx)
	}
}

// newAppealsFromEnv enables review requests when GitHub App credentials are set.
func newAppealsFromEnv(db *supabase.Client) *appeals {
	a := &appeals{db: db, dashboardURL: os.Getenv("DASHBOARD_URL"), fallbackReviewer: os.Getenv("APPEAL_REVIEWER")}
	tokens, err := ghapp.SourceFromEnv()
	if err != nil {
		log.Printf("review requests disabled: %v", err)
		return a
	}
	a.gh = newGitHubAppeals(os.Getenv("GITHUB_API_URL"), tokens)
	return a
}
