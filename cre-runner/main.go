package main

import (
	"io"
	"log"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/supabase-community/supabase-go"
)

func main() {
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

	r := gin.Default()

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
