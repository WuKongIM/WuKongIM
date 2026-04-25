package manager

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	jwt "github.com/golang-jwt/jwt/v5"
)

var errMissingBearerToken = errors.New("missing bearer token")

const managerUsernameContextKey = "manager_username"

type managerClaims struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

type userPrincipal struct {
	password    string
	permissions map[string]map[string]struct{}
	grants      []PermissionConfig
}

type authState struct {
	on        bool
	jwtSecret []byte
	jwtIssuer string
	jwtExpire time.Duration
	users     map[string]userPrincipal
}

func newAuthState(cfg AuthConfig) authState {
	state := authState{
		on:        cfg.On,
		jwtSecret: []byte(cfg.JWTSecret),
		jwtIssuer: cfg.JWTIssuer,
		jwtExpire: cfg.JWTExpire,
		users:     make(map[string]userPrincipal, len(cfg.Users)),
	}
	if state.jwtExpire <= 0 {
		state.jwtExpire = 24 * time.Hour
	}
	for _, user := range cfg.Users {
		principal := userPrincipal{}
		permissions := make(map[string]map[string]struct{}, len(user.Permissions))
		for _, permission := range user.Permissions {
			grant := PermissionConfig{
				Resource: permission.Resource,
				Actions:  append([]string(nil), permission.Actions...),
			}
			actions := make(map[string]struct{}, len(permission.Actions))
			for _, action := range permission.Actions {
				actions[action] = struct{}{}
			}
			permissions[permission.Resource] = actions
			principal.grants = append(principal.grants, grant)
		}
		principal.password = user.Password
		principal.permissions = permissions
		state.users[user.Username] = principal
	}
	return state
}

func (a authState) enabled() bool {
	return a.on
}

func (a authState) verifyCredentials(username, password string) bool {
	principal, ok := a.users[username]
	if !ok {
		return false
	}
	return principal.password == password
}

func (a authState) hasPermission(username, resource, action string) bool {
	principal, ok := a.users[username]
	if !ok {
		return false
	}
	return hasPermissionAction(principal.permissions[resource], action) ||
		hasPermissionAction(principal.permissions["*"], action)
}

func hasPermissionAction(actions map[string]struct{}, action string) bool {
	if len(actions) == 0 {
		return false
	}
	if _, ok := actions["*"]; ok {
		return true
	}
	_, ok := actions[action]
	return ok
}

func (a authState) permissionsFor(username string) []PermissionConfig {
	principal, ok := a.users[username]
	if !ok {
		return nil
	}
	out := make([]PermissionConfig, 0, len(principal.grants))
	for _, grant := range principal.grants {
		out = append(out, PermissionConfig{
			Resource: grant.Resource,
			Actions:  append([]string(nil), grant.Actions...),
		})
	}
	return out
}

func (s *Server) issueToken(username string, now time.Time) (string, error) {
	if s == nil {
		return "", fmt.Errorf("manager server is nil")
	}
	if now.IsZero() {
		now = time.Now()
	}
	claims := managerClaims{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    s.auth.jwtIssuer,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.auth.jwtExpire)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(s.auth.jwtSecret)
}

func (s *Server) requirePermission(resource, action string) gin.HandlerFunc {
	return func(c *gin.Context) {
		username, err := s.authenticateRequest(c.Request.Header.Get("Authorization"))
		if err != nil {
			jsonError(c, http.StatusUnauthorized, "unauthorized", "unauthorized")
			return
		}
		if !s.auth.hasPermission(username, resource, action) {
			jsonError(c, http.StatusForbidden, "forbidden", "forbidden")
			return
		}
		c.Set(managerUsernameContextKey, username)
		c.Next()
	}
}

func (s *Server) authenticateRequest(authorization string) (string, error) {
	tokenString, err := extractBearerToken(authorization)
	if err != nil {
		return "", err
	}
	claims, err := s.parseToken(tokenString)
	if err != nil {
		return "", err
	}
	if claims.Username == "" {
		return "", fmt.Errorf("username claim required")
	}
	return claims.Username, nil
}

func extractBearerToken(authorization string) (string, error) {
	if authorization == "" {
		return "", errMissingBearerToken
	}
	parts := strings.SplitN(strings.TrimSpace(authorization), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", errMissingBearerToken
	}
	return strings.TrimSpace(parts[1]), nil
}

func (s *Server) parseToken(tokenString string) (*managerClaims, error) {
	if s == nil {
		return nil, fmt.Errorf("manager server is nil")
	}
	claims := &managerClaims{}
	options := []jwt.ParserOption{jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()})}
	if s.auth.jwtIssuer != "" {
		options = append(options, jwt.WithIssuer(s.auth.jwtIssuer))
	}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		return s.auth.jwtSecret, nil
	}, options...)
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}
