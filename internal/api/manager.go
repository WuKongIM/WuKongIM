package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

type manager struct {
	s *Server
	wklog.Log
}

func newManager(s *Server) *manager {
	return &manager{
		s:   s,
		Log: wklog.NewWKLog("manager"),
	}
}

// route route
func (m *manager) route(r *wkhttp.WKHttp) {

	r.POST("/manager/login", m.login) // 登录

}

func (m *manager) login(c *wkhttp.Context) {

	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.ResponseError(err)
		return
	}

	if strings.TrimSpace(req.Password) == "" {
		c.ResponseError(errors.New("密码不能为空"))
		return
	}

	if strings.TrimSpace(req.Username) == "" {
		c.ResponseError(errors.New("用户名不能为空"))
		return
	}

	if strings.TrimSpace(options.G.Jwt.Secret) == "" {
		c.ResponseError(errors.New("没有配置jwt.secret"))
		return
	}

	if options.G.Auth.Auth(req.Username, req.Password) != nil {
		c.ResponseError(errors.New("用户名或密码错误"))
		return
	}

	nw := time.Now()
	expire := nw.Add(options.G.Jwt.Expire).Unix()

	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":      options.G.Jwt.Issuer, // 发行者
		"exp":      expire,               // 过期时间
		"iat":      nw.Unix(),            // 发行时间
		"username": req.Username,         // 用户名
	})
	tokenStr, err := jwtToken.SignedString([]byte(options.G.Jwt.Secret))
	if err != nil {
		m.Error("jwtToken.SignedString", zap.Error(err))
		c.ResponseError(err)
		return
	}

	persmissionStr := ""
	persmissions := options.G.Auth.Persmissions(req.Username)
	if len(persmissions) > 0 {
		persmissionStr = persmissions.Format()
	}

	c.JSON(http.StatusOK, gin.H{
		"username":    req.Username,
		"token":       tokenStr,
		"exp":         expire,
		"permissions": persmissionStr,
	})

}
