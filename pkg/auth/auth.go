package auth

import (
	"fmt"

	"github.com/WuKongIM/WuKongIM/pkg/auth/resource"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
)

type Kind string

const (
	KindNone Kind = ""
	KindJWT  Kind = "jwt"
)

type Action string

const (
	ActionNone  Action = ""
	ActionAll   Action = "*"
	ActionRead  Action = "r"
	ActionWrite Action = "w"
)

type AuthConfig struct {
	On         bool   // 是否开启鉴权
	SuperToken string // 超级token
	Kind       Kind   // 鉴权类型
	Users      []UserConfig
}

func (a AuthConfig) Auth(username string, password string) error {
	if len(a.Users) == 0 {
		return ErrAuthFailed
	}

	for _, user := range a.Users {
		if user.Username == username && user.Password == password {
			return nil
		}

	}
	return ErrAuthFailed
}

// HasPermission 是否有权限
func (a AuthConfig) HasPermission(username string, rs resource.Id, action Action) bool {

	if !a.On { // 没有开启权限
		return true
	}
	if username == "" {
		return false
	}
	if len(a.Users) == 0 {
		return false
	}
	for _, user := range a.Users {
		if user.Username == username {
			for _, permission := range user.Permissions {
				if permission.Resource == rs || permission.Resource == resource.All {
					for _, a := range permission.Actions {
						if a == ActionAll || a == action {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func (a AuthConfig) HasPermissionWithContext(ctx *wkhttp.Context, rs resource.Id, action Action) bool {
	return a.HasPermission(ctx.Username(), rs, action)
}

func (a AuthConfig) Persmissions(username string) PermissionConfigs {
	if len(a.Users) == 0 {
		return nil
	}
	for _, user := range a.Users {
		if user.Username == username {
			return user.Permissions
		}
	}
	return nil
}

type UserConfig struct {
	Username    string
	Password    string
	Permissions PermissionConfigs
}

type PermissionConfig struct {
	Resource resource.Id // 资源名称
	Actions  Actions     // 资源操作
}

type PermissionConfigs []PermissionConfig

func (p PermissionConfigs) Format() string {
	var str string
	for i, permission := range p {
		str += fmt.Sprintf("%s:%s", permission.Resource, permission.Actions.Format())
		if i != len(p)-1 {
			str += ","
		}
	}
	return str
}

type Actions []Action

func (as Actions) Format() string {

	str := ""
	for _, a := range as {
		str += string(a)
	}
	return str
}
