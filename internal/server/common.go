package server

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
)

var (
	gitCommit string
	VERSION   = "4.0.0" // 服务器版本
)

func myUptime(d time.Duration) string {
	// Just use total seconds for uptime, and display days / years
	tsecs := d / time.Second
	tmins := tsecs / 60
	thrs := tmins / 60
	tdays := thrs / 24
	tyrs := tdays / 365

	if tyrs > 0 {
		return fmt.Sprintf("%dy%dd%dh%dm%ds", tyrs, tdays%365, thrs%24, tmins%60, tsecs%60)
	}
	if tdays > 0 {
		return fmt.Sprintf("%dd%dh%dm%ds", tdays, thrs%24, tmins%60, tsecs%60)
	}
	if thrs > 0 {
		return fmt.Sprintf("%dh%dm%ds", thrs, tmins%60, tsecs%60)
	}
	if tmins > 0 {
		return fmt.Sprintf("%dm%ds", tmins, tsecs%60)
	}
	return fmt.Sprintf("%ds", tsecs)
}

const (
	aesKeyKey = "aesKey"
	aesIVKey  = "aesIV"
)

// GetFakeChannelIDWith GetFakeChannelIDWith
func GetFakeChannelIDWith(fromUID, toUID string) string {
	// TODO：这里可能会出现相等的情况 ，如果相等可以截取一部分再做hash直到不相等，后续完善
	fromUIDHash := wkutil.HashCrc32(fromUID)
	toUIDHash := wkutil.HashCrc32(toUID)
	if fromUIDHash > toUIDHash {
		return fmt.Sprintf("%s@%s", fromUID, toUID)
	}
	if fromUIDHash == toUIDHash {
		wklog.Warn("生成的fromUID的Hash和toUID的Hash是相同的！！", zap.Uint32("fromUIDHash", fromUIDHash), zap.Uint32("toUIDHash", toUIDHash), zap.String("fromUID", fromUID), zap.String("toUID", toUID))
	}

	return fmt.Sprintf("%s@%s", toUID, fromUID)
}

func GetFromUIDAndToUIDWith(channelID string) (string, string) {
	channelIDs := strings.Split(channelID, "@")
	if len(channelIDs) == 2 {
		return channelIDs[0], channelIDs[1]
	}
	return "", ""
}

// GetCommunityTopicParentChannelID 获取社区话题频道的父频道ID
func GetCommunityTopicParentChannelID(channelID string) string {
	channelIDs := strings.Split(channelID, "@")
	if len(channelIDs) == 2 {
		return channelIDs[0]
	}
	return ""
}

var (
	ErrChannelNotFound = fmt.Errorf("channel not found")
	ErrParamInvalid    = fmt.Errorf("param invalid")
)

// 加密消息
func encryptMessagePayload(payload []byte, conn wknet.Conn) ([]byte, error) {
	var (
		aesKey = conn.Value(aesKeyKey).(string)
		aesIV  = conn.Value(aesIVKey).(string)
	)
	// 加密payload
	payloadEnc, err := wkutil.AesEncryptPkcs7Base64(payload, []byte(aesKey), []byte(aesIV))
	if err != nil {
		return nil, err
	}
	return payloadEnc, nil
}

func makeMsgKey(signStr string, conn wknet.Conn) (string, error) {
	var (
		aesKey = conn.Value(aesKeyKey).(string)
		aesIV  = conn.Value(aesIVKey).(string)
	)
	// 生成MsgKey
	msgKeyBytes, err := wkutil.AesEncryptPkcs7Base64([]byte(signStr), []byte(aesKey), []byte(aesIV))
	if err != nil {
		wklog.Error("生成MsgKey失败！", zap.Error(err))
		return "", err
	}
	return wkutil.MD5(string(msgKeyBytes)), nil
}

func parseAddr(addr string) (string, int64) {
	addrPairs := strings.Split(addr, ":")
	if len(addrPairs) < 2 {
		return "", 0
	}
	portInt64, _ := strconv.ParseInt(addrPairs[len(addrPairs)-1], 10, 64)
	return addrPairs[0], portInt64
}
