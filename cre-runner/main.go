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

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/supabase-community/supabase-go"
	"github.com/token2049-decentralizing/core/cre-runner/internal/ghapp"
	"github.com/token2049-decentralizing/core/cre-runner/internal/reviewer"
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

// runnerInstance identifies this machine in cre_executions.runner_instance.
func runnerInstance() string {
	if id := os.Getenv("FLY_MACHINE_ID"); id != "" {
		return id
	}
	host, _ := os.Hostname()
	return host
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

	executions, err := setupExecutions(r, client, port)
	if err != nil {
		log.Fatalf("cre executions: %v", err)
	}

	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
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
		if executions != nil && ev.SignatureValid != nil {
			if t := prTriggerFrom(ev); t != nil {
				executions.Submit(t)
			}
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
