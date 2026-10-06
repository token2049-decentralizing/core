package main

import (
	"io"
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

func main() {
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
		log.Printf("webhook body: %s", body)
		c.String(http.StatusOK, "ack")
	})

	if err := r.Run(":8080"); err != nil {
		log.Fatal(err)
	}
}