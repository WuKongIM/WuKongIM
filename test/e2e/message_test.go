package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_Message_SingleChat 测试单聊消息收发 (WebSocket)
func TestE2E_Message_SingleChat(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uidA := "userA_" + wkutil.GenUUID()[:8]
	uidB := "userB_" + wkutil.GenUUID()[:8]

	clientA := newTestWSClient(t, testServerInstance.wsURL)
	defer clientA.Close()
	err := clientA.Connect(uidA, "tokenA")
	require.NoError(t, err)

	clientB := newTestWSClient(t, testServerInstance.wsURL)
	defer clientB.Close()
	err = clientB.Connect(uidB, "tokenB")
	require.NoError(t, err)

	payload := []byte("hello userB from userA")
	err = clientA.SendMessage(uidB, wkproto.ChannelTypePerson, payload)
	require.NoError(t, err)

	recv, err := clientB.WaitForRecv(wsTimeout)
	require.NoError(t, err)
	assert.Equal(t, uidA, recv.FromUID)
	assert.Equal(t, payload, recv.Payload)
}

// TestE2E_Message_SingleChat_API 测试单聊消息发送 (API -> WebSocket)
func TestE2E_Message_SingleChat_API(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uidA := "userA_api_" + wkutil.GenUUID()[:8]
	uidB := "userB_api_" + wkutil.GenUUID()[:8]

	clientB := newTestWSClient(t, testServerInstance.wsURL)
	defer clientB.Close()
	err := clientB.Connect(uidB, "tokenB")
	require.NoError(t, err)

	payload := []byte("hello from API")
	reqBody := map[string]interface{}{
		"header": map[string]interface{}{
			"no_persist": 0,
			"red_dot":    1,
			"sync_once":  0,
		},
		"from_uid":     uidA,
		"channel_id":   uidB,
		"channel_type": wkproto.ChannelTypePerson,
		"payload":      payload,
	}

	resp, err := apiPost(testServerInstance.apiURL+"/message/send", reqBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	recv, err := clientB.WaitForRecv(wsTimeout)
	require.NoError(t, err)
	assert.Equal(t, uidA, recv.FromUID)
	assert.Equal(t, payload, recv.Payload)
}

// TestE2E_Message_GroupChat 测试群聊消息收发
func TestE2E_Message_GroupChat(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	groupId := "group_" + wkutil.GenUUID()[:8]
	uidA := "userA_group_" + wkutil.GenUUID()[:8]
	uidB := "userB_group_" + wkutil.GenUUID()[:8]

	// 1. 创建频道并添加订阅者
	reqBody := map[string]interface{}{
		"channel_id":   groupId,
		"channel_type": wkproto.ChannelTypeGroup,
		"subscribers":  []string{uidA, uidB},
	}
	resp, err := apiPost(testServerInstance.apiURL+"/channel", reqBody)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	clientA := newTestWSClient(t, testServerInstance.wsURL)
	defer clientA.Close()
	err = clientA.Connect(uidA, "tokenA")
	require.NoError(t, err)

	clientB := newTestWSClient(t, testServerInstance.wsURL)
	defer clientB.Close()
	err = clientB.Connect(uidB, "tokenB")
	require.NoError(t, err)

	payload := []byte("hello group from userA")
	err = clientA.SendMessage(groupId, wkproto.ChannelTypeGroup, payload)
	require.NoError(t, err)

	recv, err := clientB.WaitForRecv(wsTimeout)
	require.NoError(t, err)
	assert.Equal(t, uidA, recv.FromUID)
	assert.Equal(t, groupId, recv.ChannelID)
	assert.Equal(t, wkproto.ChannelTypeGroup, recv.ChannelType)
	assert.Equal(t, payload, recv.Payload)
}

// TestE2E_Message_BatchSend 测试批量发送消息
func TestE2E_Message_BatchSend(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uidA := "userA_batch_" + wkutil.GenUUID()[:8]
	uidB := "userB_batch_" + wkutil.GenUUID()[:8]
	uidC := "userC_batch_" + wkutil.GenUUID()[:8]

	clientB := newTestWSClient(t, testServerInstance.wsURL)
	defer clientB.Close()
	err := clientB.Connect(uidB, "tokenB")
	require.NoError(t, err)

	clientC := newTestWSClient(t, testServerInstance.wsURL)
	defer clientC.Close()
	err = clientC.Connect(uidC, "tokenC")
	require.NoError(t, err)

	payload := []byte("batch message")
	reqBody := map[string]interface{}{
		"from_uid":    uidA,
		"subscribers": []string{uidB, uidC},
		"payload":     payload,
	}

	resp, err := apiPost(testServerInstance.apiURL+"/message/sendbatch", reqBody)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	recvB, err := clientB.WaitForRecv(wsTimeout)
	require.NoError(t, err)
	assert.Equal(t, payload, recvB.Payload)

	recvC, err := clientC.WaitForRecv(wsTimeout)
	require.NoError(t, err)
	assert.Equal(t, payload, recvC.Payload)
}

// TestE2E_Message_Sync 测试频道消息同步
func TestE2E_Message_Sync(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testServerInstance)

	uidA := "userA_sync_" + wkutil.GenUUID()[:8]
	uidB := "userB_sync_" + wkutil.GenUUID()[:8]

	clientA := newTestWSClient(t, testServerInstance.wsURL)
	defer clientA.Close()
	err := clientA.Connect(uidA, "tokenA")
	require.NoError(t, err)

	// 发送 3 条消息
	for i := 1; i <= 3; i++ {
		payload := []byte(fmt.Sprintf("msg %d", i))
		err = clientA.SendMessage(uidB, wkproto.ChannelTypePerson, payload)
		require.NoError(t, err)
		_, err = clientA.WaitForSendack(wsTimeout)
		require.NoError(t, err)
	}

	// 同步消息
	reqBody := map[string]interface{}{
		"login_uid":         uidA,
		"channel_id":        uidB,
		"channel_type":      wkproto.ChannelTypePerson,
		"start_message_seq": 0,
		"limit":             10,
		"pull_mode":         0,
	}

	resp, err := apiPost(testServerInstance.apiURL+"/channel/messagesync", reqBody)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var syncResp struct {
		Messages []interface{} `json:"messages"`
	}
	err = json.NewDecoder(resp.Body).Decode(&syncResp)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(syncResp.Messages), 3) // 因为之前可能已经有消息了
}

