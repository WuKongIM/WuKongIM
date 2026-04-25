package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

const openCORSAllowHeaders = "Origin, Content-Type, Content-Length, Accept, Authorization, Token, X-Requested-With"
const openCORSAllowMethods = "GET, POST, PUT, PATCH, DELETE, OPTIONS"
const openCORSExposeHeaders = "Content-Length, Content-Type"

func openCORSMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Headers", openCORSAllowHeaders)
		c.Header("Access-Control-Allow-Methods", openCORSAllowMethods)
		c.Header("Access-Control-Expose-Headers", openCORSExposeHeaders)
		c.Header("Access-Control-Max-Age", "86400")

		if c.Request != nil && c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
