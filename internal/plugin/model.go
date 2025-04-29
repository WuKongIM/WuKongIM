package plugin

import (
	"io"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/types"
	"github.com/WuKongIM/WuKongIM/internal/types/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	lru "github.com/hashicorp/golang-lru/v2"
)

type pluginResp struct {
	NodeId         uint64                      `json:"node_id,omitempty"`
	No             string                      `json:"no,omitempty"`
	Name           string                      `json:"name,omitempty"`
	ConfigTemplate *pluginproto.ConfigTemplate `json:"config_template,omitempty"`
	Config         map[string]interface{}      `json:"config,omitempty"`
	CreatedAt      *time.Time                  `json:"created_at,omitempty"`
	UpdatedAt      *time.Time                  `json:"updated_at,omitempty"`
	Status         types.PluginStatus          `json:"status,omitempty"`
	Version        string                      `json:"version,omitempty"`
	Methods        []string                    `json:"methods,omitempty"`
	Priority       uint32                      `json:"priority,omitempty"`
	IsAI           uint8                       `json:"is_ai,omitempty"` // 是否是机器人插件
}

// 默认隐藏secret字段的值
const secretHidden = "******"

func newPluginResp(p wkdb.Plugin) *pluginResp {
	cfgTemplate := &pluginproto.ConfigTemplate{}
	if len(p.ConfigTemplate) > 0 {
		_ = cfgTemplate.Unmarshal(p.ConfigTemplate)

		// 抹掉secret字段的值，只返回是否有值 1.有值 0.无值
		for _, field := range cfgTemplate.Fields {
			if field.Type == pluginproto.FieldTypeSecret.String() {
				if _, ok := p.Config[field.Name]; ok {
					p.Config[field.Name] = secretHidden
				}
			}
		}
	}

	isAI := uint8(0)
	if len(p.Methods) > 0 && wkutil.ArrayContains(p.Methods, types.PluginReceive.String()) {
		isAI = 1
	}

	return &pluginResp{
		No:             p.No,
		Name:           p.Name,
		ConfigTemplate: cfgTemplate,
		Config:         p.Config,
		CreatedAt:      p.CreatedAt,
		UpdatedAt:      p.UpdatedAt,
		Version:        p.Version,
		Methods:        p.Methods,
		Priority:       p.Priority,
		IsAI:           isAI,
	}
}

func BindJSON(obj any, c *wkhttp.Context) ([]byte, error) {
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, err
	}
	if err := wkutil.ReadJSONByByte(bodyBytes, obj); err != nil {
		return nil, err
	}
	return bodyBytes, nil
}

type pluginUserResp struct {
	PluginNo  string `json:"plugin_no"`
	Uid       string `json:"uid"`
	CreatedAt uint64 `json:"created_at"`
	UpdatedAt uint64 `json:"updated_at"`
}

func newPluginUserResp(pu wkdb.PluginUser) *pluginUserResp {

	var createdAt uint64
	if pu.CreatedAt != nil {
		createdAt = uint64(pu.CreatedAt.UnixNano())
	}

	var updatedAt uint64
	if pu.UpdatedAt != nil {
		updatedAt = uint64(pu.UpdatedAt.UnixNano())
	}

	return &pluginUserResp{
		PluginNo:  pu.PluginNo,
		Uid:       pu.Uid,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
}

type userPluginBucket struct {
	cache     *lru.Cache[string, string]
	index     int
	isAiCache *lru.Cache[string, bool] // 缓存用户是否是AI
}

func newUserPluginBucket(index int) *userPluginBucket {
	cache, _ := lru.New[string, string](1000)
	isAiCache, _ := lru.New[string, bool](1000)
	return &userPluginBucket{
		cache:     cache,
		index:     index,
		isAiCache: isAiCache,
	}
}

func (u *userPluginBucket) add(key string, value string) {
	u.cache.Add(key, value)
}

func (u *userPluginBucket) get(key string) (string, bool) {
	v, ok := u.cache.Get(key)
	if !ok {
		return "", false
	}
	return v, true
}

func (u *userPluginBucket) remove(key string) {
	u.cache.Remove(key)
}

func (u *userPluginBucket) isAi(key string) (bool, bool) {
	v, ok := u.isAiCache.Get(key)
	return v, ok
}

func (u *userPluginBucket) setIsAi(key string, value bool) {
	u.isAiCache.Add(key, value)
}

func (u *userPluginBucket) removeIsAi(key string) {
	u.isAiCache.Remove(key)
}
