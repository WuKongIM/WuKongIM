package e2e

import (
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_API_HealthCheck 测试健康检查接口
func TestE2E_API_HealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	resp, err := apiGet(testServerInstance.apiURL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestE2E_API_ChannelCreateAndDelete 测试创建和删除频道
func TestE2E_API_ChannelCreateAndDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	channelId := "api_channel_" + wkutil.GenUUID()[:8]

	// 创建
	reqBody := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
	}
	resp, err := apiPost(testServerInstance.apiURL+"/channel", reqBody)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 删除
	reqBodyDel := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
	}
	resp, err = apiPost(testServerInstance.apiURL+"/channel/delete", reqBodyDel)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestE2E_API_SubscriberManage 测试订阅者管理
func TestE2E_API_SubscriberManage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	channelId := "api_sub_channel_" + wkutil.GenUUID()[:8]
	uid := "api_sub_user_" + wkutil.GenUUID()[:8]

	// 添加
	reqBodyAdd := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
		"subscribers":  []string{uid},
	}
	resp, err := apiPost(testServerInstance.apiURL+"/channel/subscriber_add", reqBodyAdd)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 移除
	reqBodyRem := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
		"subscribers":  []string{uid},
	}
	resp, err = apiPost(testServerInstance.apiURL+"/channel/subscriber_remove", reqBodyRem)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestE2E_API_BlacklistManage 测试黑名单管理
func TestE2E_API_BlacklistManage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	channelId := "api_black_channel_" + wkutil.GenUUID()[:8]
	uid := "api_black_user_" + wkutil.GenUUID()[:8]

	// 添加
	reqBodyAdd := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
		"uids":         []string{uid},
	}
	resp, err := apiPost(testServerInstance.apiURL+"/channel/blacklist_add", reqBodyAdd)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 移除
	reqBodyRem := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
		"uids":         []string{uid},
	}
	resp, err = apiPost(testServerInstance.apiURL+"/channel/blacklist_remove", reqBodyRem)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestE2E_API_WhitelistManage 测试白名单管理
func TestE2E_API_WhitelistManage(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	channelId := "api_white_channel_" + wkutil.GenUUID()[:8]
	uid := "api_white_user_" + wkutil.GenUUID()[:8]

	// 添加
	reqBodyAdd := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
		"uids":         []string{uid},
	}
	resp, err := apiPost(testServerInstance.apiURL+"/channel/whitelist_add", reqBodyAdd)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 移除
	reqBodyRem := map[string]interface{}{
		"channel_id":   channelId,
		"channel_type": wkproto.ChannelTypeGroup,
		"uids":         []string{uid},
	}
	resp, err = apiPost(testServerInstance.apiURL+"/channel/whitelist_remove", reqBodyRem)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

