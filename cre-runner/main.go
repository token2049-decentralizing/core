package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"github.com/supabase-community/supabase-go"
)

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
		log.Printf("GITHUB_WEBHOOK_SECRET not set; webhook signatures will not be verified")
	}

	corsOrigins := strings.Split(os.Getenv("CORS_ALLOWED_ORIGINS"), ",")
	for i := range corsOrigins {
		corsOrigins[i] = strings.TrimSpace(corsOrigins[i])
	}
	if os.Getenv("CORS_ALLOWED_ORIGINS") == "" {
		corsOrigins = []string{"*"}
	}

	// PR evaluations via the CRE runner; off unless EVALUATOR_URL is set.
	store := supabaseEvalStore{db: client}
	var evals *evaluations
	if url := os.Getenv("EVALUATOR_URL"); url != "" {
		secret := os.Getenv("EVALUATOR_SECRET")
		if len(secret) < 16 {
			log.Fatalf("EVALUATOR_SECRET must be at least 16 characters (the runner's RUNNER_SHARED_SECRET)")
		}
		concurrency, err := strconv.Atoi(os.Getenv("EVALUATION_CONCURRENCY"))
		if err != nil || concurrency <= 0 {
			concurrency = 2
		}
		evals = newEvaluations(store, newEvaluatorClient(url, secret), concurrency)
		if err := store.failInterrupted(); err != nil {
			log.Printf("evaluations: close interrupted runs: %v", err)
		}
		log.Printf("evaluations: enabled, runner %s", url)
	} else {
		log.Printf("EVALUATOR_URL not set; PR evaluations are disabled")
	}

	r := gin.Default()
	r.Use(corsMiddleware(corsOrigins))
	registerAPI(r, client)
	registerEvaluationsAPI(r, evals, store, os.Getenv("ADMIN_API_KEY"))

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
		// Only verified deliveries may trigger evaluations (they cost LLM calls and can settle rewards).
		if evals != nil {
			if ev.SignatureValid == nil {
				log.Printf("webhook: not evaluating unsigned delivery %s (set GITHUB_WEBHOOK_SECRET)", ev.DeliveryID)
			} else {
				go evals.fromWebhook(ev) // Runs take minutes; GitHub expects a reply within 10s.
			}
		}
		c.String(http.StatusOK, "ack")
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := r.Run(":" + port); err != nil {
		log.Fatal(err)
	}
}
