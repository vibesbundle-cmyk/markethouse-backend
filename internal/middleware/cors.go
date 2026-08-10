package middleware

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

// CORS allows the Flutter web app (hosted on a different origin) to call the
// API. Allowed origins come from the CORS_ORIGINS env var, comma-separated,
// with localhost/dev origins always allowed.
func CORS() gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" {
			c.Next()
			return
		}

		allowed := isAllowedOrigin(origin)
		if allowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Credentials", "true")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}

func isAllowedOrigin(origin string) bool {
	// Local dev is always allowed.
	for _, dev := range []string{"http://localhost", "http://127.0.0.1"} {
		if origin == dev || len(origin) >= len(dev) && origin[:len(dev)] == dev {
			return true
		}
	}
	// Production origins configured via CORS_ORIGINS (comma-separated).
	for _, o := range splitComma(envOr("CORS_ORIGINS", "")) {
		if o == origin {
			return true
		}
	}
	return false
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if v := trimSpace(s[start:i]); v != "" {
				out = append(out, v)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}

func envOr(key, fallback string) string {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	return v
}
