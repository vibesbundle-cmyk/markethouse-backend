package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"markethouse/pkg/utils"
)

func JWTAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {

		authHeader := c.GetHeader("Authorization")
		var tokenString string

		if authHeader != "" {
			parts := strings.Split(authHeader, " ")
			if len(parts) != 2 || parts[0] != "Bearer" {
				c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid auth format"})
				c.Abort()
				return
			}
			tokenString = parts[1]
		} else if q := c.Query("token"); q != "" {
			// WebSocket clients can't set custom headers, so /ws connects
			// with ?token=... instead of an Authorization header.
			tokenString = q
		} else {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "missing token"})
			c.Abort()
			return
		}

		claims, err := utils.ValidateToken(tokenString)
		if err != nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			c.Abort()
			return
		}

		// ✅ clean + safe
		c.Set("user_id", claims.UserID)
		c.Set("email", claims.Email)

		c.Next()
	}
}