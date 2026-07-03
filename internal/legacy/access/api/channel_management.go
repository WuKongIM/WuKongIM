package api

import (
	"net/http"
	"strconv"
	"strings"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/legacy/usecase/channel"
	"github.com/WuKongIM/WuKongIM/pkg/protocol/frame"
	"github.com/gin-gonic/gin"
)

type channelInfoRequest struct {
	ChannelID     string `json:"channel_id"`
	ChannelType   uint8  `json:"channel_type"`
	Large         int    `json:"large"`
	Ban           int    `json:"ban"`
	Disband       int    `json:"disband"`
	SendBan       int    `json:"send_ban"`
	AllowStranger int    `json:"allow_stranger"`
}

type channelUpsertRequest struct {
	channelInfoRequest
	Reset       int      `json:"reset"`
	Subscribers []string `json:"subscribers"`
}

type channelSubscriberRequest struct {
	ChannelID      string   `json:"channel_id"`
	ChannelType    uint8    `json:"channel_type"`
	Reset          int      `json:"reset"`
	TempSubscriber int      `json:"temp_subscriber"`
	Subscribers    []string `json:"subscribers"`
}

type tmpChannelSubscriberRequest struct {
	ChannelID string   `json:"channel_id"`
	UIDs      []string `json:"uids"`
}

type channelMemberRequest struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType uint8    `json:"channel_type"`
	UIDs        []string `json:"uids"`
}

type channelKeyRequest struct {
	ChannelID   string `json:"channel_id"`
	ChannelType uint8  `json:"channel_type"`
}

func (s *Server) handleChannelUpsert(c *gin.Context) {
	var req channelUpsertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if errMsg := validateChannelUpsert(req); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson && len(req.Subscribers) > 0 {
		writeLegacyJSONError(c, "不支持个人频道添加订阅者！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	err := s.channels.Upsert(c.Request.Context(), channelusecase.UpsertCommand{
		Info:        req.toInfo(),
		Reset:       req.Reset == 1,
		Subscribers: req.Subscribers,
	})
	writeLegacyMutationResult(c, err)
}

func (s *Server) handleChannelInfo(c *gin.Context) {
	var req channelInfoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.UpdateInfo(c.Request.Context(), req.toInfo()))
}

func (s *Server) handleChannelDelete(c *gin.Context) {
	var req channelKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeLegacyJSONError(c, "个人频道不支持添加订阅者！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.Delete(c.Request.Context(), req.toKey()))
}

func (s *Server) handleChannelSubscriberAdd(c *gin.Context) {
	var req channelSubscriberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if errMsg := validateSubscriberChange(req); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeLegacyJSONError(c, "个人频道不支持添加订阅者！")
		return
	}
	if req.TempSubscriber == 1 {
		writeLegacyJSONError(c, "新版本临时订阅者已不支持！")
		return
	}
	if req.ChannelType == 0 {
		req.ChannelType = frame.ChannelTypeGroup
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.AddSubscribers(c.Request.Context(), req.toSubscriberCommand()))
}

func (s *Server) handleChannelSubscriberRemove(c *gin.Context) {
	var req channelSubscriberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if errMsg := validateSubscriberChange(req); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeLegacyJSONError(c, "个人频道不支持添加订阅者！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.RemoveSubscribers(c.Request.Context(), req.toSubscriberCommand()))
}

func (s *Server) handleChannelSubscriberRemoveAll(c *gin.Context) {
	var req channelKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if errMsg := validateChannelKey(req); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeLegacyJSONError(c, "个人频道不支持此操作！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.RemoveAllSubscribers(c.Request.Context(), req.toKey()))
}

func (s *Server) handleTmpChannelSubscriberSet(c *gin.Context) {
	var req tmpChannelSubscriberRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return
	}
	if errMsg := validateTmpSubscriberSet(req); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.SetTempSubscribers(c.Request.Context(), channelusecase.TempSubscriberCommand{
		ChannelID: req.ChannelID,
		UIDs:      req.UIDs,
	}))
}

func (s *Server) handleChannelDenylistAdd(c *gin.Context) {
	var req channelMemberRequest
	if !bindLegacyChannelMember(c, &req, true) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.AddDenylist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelDenylistSet(c *gin.Context) {
	var req channelMemberRequest
	if !bindLegacyChannelMemberSet(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.SetDenylist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelDenylistRemove(c *gin.Context) {
	var req channelMemberRequest
	if !bindLegacyChannelMember(c, &req, false) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.RemoveDenylist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelDenylistRemoveAll(c *gin.Context) {
	var req channelKeyRequest
	if !bindLegacyChannelKey(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.RemoveAllDenylist(c.Request.Context(), req.toKey()))
}

func (s *Server) handleChannelAllowlistAdd(c *gin.Context) {
	var req channelMemberRequest
	if !bindLegacyAllowMember(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.AddAllowlist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelAllowlistSet(c *gin.Context) {
	var req channelMemberRequest
	if !bindLegacyChannelMemberSet(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.SetAllowlist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelAllowlistRemove(c *gin.Context) {
	var req channelMemberRequest
	if !bindLegacyAllowMember(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.RemoveAllowlist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelAllowlistRemoveAll(c *gin.Context) {
	var req channelKeyRequest
	if !bindLegacyChannelKey(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	writeLegacyMutationResult(c, s.channels.RemoveAllAllowlist(c.Request.Context(), req.toKey()))
}

func (s *Server) handleChannelAllowlistGet(c *gin.Context) {
	channelType, _ := strconv.ParseUint(c.Query("channel_type"), 10, 8)
	key := channelusecase.ChannelKey{
		ChannelID:   c.Query("channel_id"),
		ChannelType: uint8(channelType),
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	result, err := s.channels.ListAllowlist(c.Request.Context(), key)
	if err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, result.Members)
}

func bindLegacyChannelMember(c *gin.Context, req *channelMemberRequest, requireUIDs bool) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return false
	}
	if errMsg := validateChannelMember(*req, requireUIDs); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return false
	}
	return true
}

func bindLegacyChannelMemberSet(c *gin.Context, req *channelMemberRequest) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return false
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		writeLegacyJSONError(c, "频道ID不能为空！")
		return false
	}
	return true
}

func bindLegacyAllowMember(c *gin.Context, req *channelMemberRequest) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return false
	}
	if errMsg := validateAllowMember(*req); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return false
	}
	return true
}

func bindLegacyChannelKey(c *gin.Context, req *channelKeyRequest) bool {
	if err := c.ShouldBindJSON(req); err != nil {
		writeLegacyJSONError(c, "数据格式有误！")
		return false
	}
	if errMsg := validateChannelKey(*req); errMsg != "" {
		writeLegacyJSONError(c, errMsg)
		return false
	}
	return true
}

func (r channelInfoRequest) toInfo() channelusecase.Info {
	return channelusecase.Info{
		ChannelID:     r.ChannelID,
		ChannelType:   r.ChannelType,
		Large:         r.Large == 1,
		Ban:           r.Ban == 1,
		Disband:       r.Disband == 1,
		SendBan:       r.SendBan == 1,
		AllowStranger: r.AllowStranger == 1,
	}
}

func (r channelSubscriberRequest) toSubscriberCommand() channelusecase.SubscriberCommand {
	return channelusecase.SubscriberCommand{
		ChannelID:   r.ChannelID,
		ChannelType: r.ChannelType,
		Reset:       r.Reset == 1,
		Subscribers: r.Subscribers,
	}
}

func (r channelKeyRequest) toKey() channelusecase.ChannelKey {
	return channelusecase.ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType}
}

func (r channelMemberRequest) toMemberCommand() channelusecase.MemberCommand {
	return channelusecase.MemberCommand{
		ChannelKey: channelusecase.ChannelKey{ChannelID: r.ChannelID, ChannelType: r.ChannelType},
		UIDs:       r.UIDs,
	}
}

func validateChannelUpsert(req channelUpsertRequest) string {
	if strings.TrimSpace(req.ChannelID) == "" {
		return "频道ID不能为空！"
	}
	if req.ChannelType == 0 {
		return "频道类型错误！"
	}
	if containsLegacySpecialChar(req.ChannelID) {
		return "频道ID不能包含特殊字符！"
	}
	return ""
}

func validateSubscriberChange(req channelSubscriberRequest) string {
	if strings.TrimSpace(req.ChannelID) == "" {
		return "频道ID不能为空！"
	}
	if containsLegacySpecialChar(req.ChannelID) {
		return "频道ID不能包含特殊字符！"
	}
	if stringArrayIsEmpty(req.Subscribers) {
		return "订阅者不能为空！"
	}
	return ""
}

func validateTmpSubscriberSet(req tmpChannelSubscriberRequest) string {
	if req.ChannelID == "" {
		return "channel_id不能为空！"
	}
	if containsLegacySpecialChar(req.ChannelID) {
		return "频道ID不能包含特殊字符！"
	}
	if len(req.UIDs) == 0 {
		return "uids不能为空！"
	}
	return ""
}

func validateChannelMember(req channelMemberRequest, requireUIDs bool) string {
	if req.ChannelID == "" {
		return "channel_id不能为空！"
	}
	if req.ChannelType == 0 {
		return "频道类型不能为0！"
	}
	if requireUIDs && len(req.UIDs) == 0 {
		return "uids不能为空！"
	}
	if !requireUIDs && len(req.UIDs) == 0 {
		return "uids不能为空！"
	}
	return ""
}

func validateAllowMember(req channelMemberRequest) string {
	if req.ChannelID == "" {
		return "channel_id不能为空！"
	}
	if containsLegacySpecialChar(req.ChannelID) {
		return "频道ID不能包含特殊字符！"
	}
	if req.ChannelType == 0 {
		return "频道类型不能为0！"
	}
	if stringArrayIsEmpty(req.UIDs) {
		return "uids不能为空！"
	}
	return ""
}

func validateChannelKey(req channelKeyRequest) string {
	if req.ChannelID == "" {
		return "channel_id不能为空！"
	}
	if req.ChannelType == 0 {
		return "频道类型不能为0！"
	}
	return ""
}

func stringArrayIsEmpty(values []string) bool {
	if len(values) == 0 {
		return true
	}
	emptyCount := 0
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			emptyCount++
		}
	}
	return emptyCount >= len(values)
}

func containsLegacySpecialChar(value string) bool {
	return strings.Contains(value, "@") || strings.Contains(value, "#") || strings.Contains(value, "&")
}

func (s *Server) requireChannelUsecase() error {
	if s == nil || s.channels == nil {
		return channelusecase.ErrStoreRequired
	}
	return nil
}

func writeLegacyMutationResult(c *gin.Context, err error) {
	if err != nil {
		writeLegacyJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}
