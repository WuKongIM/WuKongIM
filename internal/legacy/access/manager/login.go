package manager

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Username    string               `json:"username"`
	TokenType   string               `json:"token_type"`
	AccessToken string               `json:"access_token"`
	ExpiresIn   int64                `json:"expires_in"`
	ExpiresAt   time.Time            `json:"expires_at"`
	Permissions []loginPermissionDTO `json:"permissions"`
}

type loginPermissionDTO struct {
	Resource string   `json:"resource"`
	Actions  []string `json:"actions"`
}

func (s *Server) handleLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonError(c, http.StatusBadRequest, "invalid_request", "invalid request")
		return
	}
	if !s.auth.verifyCredentials(req.Username, req.Password) {
		jsonError(c, http.StatusUnauthorized, "invalid_credentials", "invalid credentials")
		return
	}

	now := time.Now()
	token, err := s.issueToken(req.Username, now)
	if err != nil {
		jsonError(c, http.StatusInternalServerError, "internal_error", "failed to issue token")
		return
	}
	expiresIn := int64(s.auth.jwtExpire / time.Second)
	c.JSON(http.StatusOK, loginResponse{
		Username:    req.Username,
		TokenType:   "Bearer",
		AccessToken: token,
		ExpiresIn:   expiresIn,
		ExpiresAt:   now.Add(s.auth.jwtExpire),
		Permissions: loginPermissionDTOs(s.auth.permissionsFor(req.Username)),
	})
}

func loginPermissionDTOs(grants []PermissionConfig) []loginPermissionDTO {
	out := make([]loginPermissionDTO, 0, len(grants))
	for _, grant := range grants {
		out = append(out, loginPermissionDTO{
			Resource: grant.Resource,
			Actions:  append([]string(nil), grant.Actions...),
		})
	}
	return out
}
