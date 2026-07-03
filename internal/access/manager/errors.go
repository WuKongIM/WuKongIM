package manager

import "github.com/gin-gonic/gin"

type errorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}

func jsonError(c *gin.Context, status int, code, message string) {
	if message == "" {
		message = code
	}
	c.AbortWithStatusJSON(status, errorResponse{
		Error:   code,
		Message: message,
	})
}
