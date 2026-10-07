package main

import (
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
)

// corsMiddleware allows browser requests from the given origins ("*" allows any).
func corsMiddleware(allowed []string) gin.HandlerFunc {
	allowAny := slices.Contains(allowed, "*")
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin != "" && (allowAny || slices.Contains(allowed, origin)) {
			h := c.Writer.Header()
			if allowAny {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
				h.Add("Vary", "Origin")
			}
			h.Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			h.Set("Access-Control-Max-Age", "86400")
		}
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
