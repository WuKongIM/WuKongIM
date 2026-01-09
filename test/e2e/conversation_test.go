package e2e

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_Conversation_Sync 测试会话同步
func TestE2E_Conversation_Sync(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uid := "user_conv_sync_" + wkutil.GenUUID()[:8]
	uidTarget := "target_" + wkutil.GenUUID()[:8]

	// 发送消息以产生会话
	reqBodyMsg := map[string]interface{}{
		"from_uid":     uidTarget,
		"channel_id":   uid,
		"channel_type": wkproto.ChannelTypePerson,
		"payload":      []byte("hello"),
	}
	resp, err := apiPost(testServerInstance.apiURL+"/message/send", reqBodyMsg)
	require.NoError(t, err)
	resp.Body.Close()

	// 同步会话
	reqBody := map[string]interface{}{
		"uid":               uid,
		"version":           0,
		"msg_count":         1,
		"conversation_type": wkdb.ConversationTypeChat,
	}
	resp, err = apiPost(testServerInstance.apiURL+"/conversation/sync", reqBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var convs []interface{}
	err = json.NewDecoder(resp.Body).Decode(&convs)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(convs), 1)
}

// TestE2E_Conversation_ClearUnread 测试清空未读
func TestE2E_Conversation_ClearUnread(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uid := "user_clear_unread_" + wkutil.GenUUID()[:8]
	uidTarget := "target_" + wkutil.GenUUID()[:8]

	// 发送消息
	reqBodyMsg := map[string]interface{}{
		"from_uid":     uidTarget,
		"channel_id":   uid,
		"channel_type": wkproto.ChannelTypePerson,
		"payload":      []byte("unread message"),
	}
	resp, err := apiPost(testServerInstance.apiURL+"/message/send", reqBodyMsg)
	require.NoError(t, err)
	resp.Body.Close()

	// 清空未读
	reqBody := map[string]interface{}{
		"uid":          uid,
		"channel_id":   uidTarget,
		"channel_type": wkproto.ChannelTypePerson,
	}
	resp, err = apiPost(testServerInstance.apiURL+"/conversations/clearUnread", reqBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestE2E_Conversation_SetUnread 测试设置未读
func TestE2E_Conversation_SetUnread(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uid := "user_set_unread_" + wkutil.GenUUID()[:8]
	uidTarget := "target_" + wkutil.GenUUID()[:8]

	// 设置未读
	reqBody := map[string]interface{}{
		"uid":          uid,
		"channel_id":   uidTarget,
		"channel_type": wkproto.ChannelTypePerson,
		"unread":       10,
	}
	resp, err := apiPost(testServerInstance.apiURL+"/conversations/setUnread", reqBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestE2E_Conversation_Delete 测试删除会话
func TestE2E_Conversation_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uid := "user_del_conv_" + wkutil.GenUUID()[:8]
	uidTarget := "target_" + wkutil.GenUUID()[:8]

	// 删除会话
	reqBody := map[string]interface{}{
		"uid":          uid,
		"channel_id":   uidTarget,
		"channel_type": wkproto.ChannelTypePerson,
	}
	resp, err := apiPost(testServerInstance.apiURL+"/conversations/delete", reqBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

