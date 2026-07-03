package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
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

func (s *Server) registerChannelRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	s.engine.POST("/channel", s.handleChannelUpsert)
	s.engine.POST("/channel/info", s.handleChannelInfo)
	s.engine.POST("/channel/delete", s.handleChannelDelete)
	s.engine.POST("/channel/subscriber_add", s.handleChannelSubscriberAdd)
	s.engine.POST("/channel/subscriber_remove", s.handleChannelSubscriberRemove)
	s.engine.POST("/channel/subscriber_remove_all", s.handleChannelSubscriberRemoveAll)
	s.engine.POST("/tmpchannel/subscriber_set", s.handleTmpChannelSubscriberSet)
	s.engine.POST("/channel/blacklist_add", s.handleChannelDenylistAdd)
	s.engine.POST("/channel/blacklist_set", s.handleChannelDenylistSet)
	s.engine.POST("/channel/blacklist_remove", s.handleChannelDenylistRemove)
	s.engine.POST("/channel/blacklist_remove_all", s.handleChannelDenylistRemoveAll)
	s.engine.POST("/channel/whitelist_add", s.handleChannelAllowlistAdd)
	s.engine.POST("/channel/whitelist_set", s.handleChannelAllowlistSet)
	s.engine.POST("/channel/whitelist_remove", s.handleChannelAllowlistRemove)
	s.engine.POST("/channel/whitelist_remove_all", s.handleChannelAllowlistRemoveAll)
	s.engine.GET("/channel/whitelist", s.handleChannelAllowlistGet)
}

func (s *Server) handleChannelUpsert(c *gin.Context) {
	var req channelUpsertRequest
	if !bindJSON(c, &req) {
		return
	}
	if errMsg := validateChannelUpsert(req); errMsg != "" {
		writeJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson && len(req.Subscribers) > 0 {
		writeJSONError(c, "不支持个人频道添加订阅者！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.Upsert(c.Request.Context(), channelusecase.UpsertCommand{
		Info:        req.toInfo(),
		Reset:       req.Reset == 1,
		Subscribers: req.Subscribers,
	}))
}

func (s *Server) handleChannelInfo(c *gin.Context) {
	var req channelInfoRequest
	if !bindJSON(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.UpdateInfo(c.Request.Context(), req.toInfo()))
}

func (s *Server) handleChannelDelete(c *gin.Context) {
	var req channelKeyRequest
	if !bindJSON(c, &req) {
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeJSONError(c, "个人频道不支持添加订阅者！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.Delete(c.Request.Context(), req.toKey()))
}

func (s *Server) handleChannelSubscriberAdd(c *gin.Context) {
	var req channelSubscriberRequest
	if !bindJSON(c, &req) {
		return
	}
	if errMsg := validateSubscriberChange(req); errMsg != "" {
		writeJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeJSONError(c, "个人频道不支持添加订阅者！")
		return
	}
	if req.TempSubscriber == 1 {
		writeJSONError(c, "新版本临时订阅者已不支持！")
		return
	}
	if req.ChannelType == 0 {
		req.ChannelType = frame.ChannelTypeGroup
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.AddSubscribers(c.Request.Context(), req.toSubscriberCommand()))
}

func (s *Server) handleChannelSubscriberRemove(c *gin.Context) {
	var req channelSubscriberRequest
	if !bindJSON(c, &req) {
		return
	}
	if errMsg := validateSubscriberChange(req); errMsg != "" {
		writeJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeJSONError(c, "个人频道不支持添加订阅者！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.RemoveSubscribers(c.Request.Context(), req.toSubscriberCommand()))
}

func (s *Server) handleChannelSubscriberRemoveAll(c *gin.Context) {
	var req channelKeyRequest
	if !bindJSON(c, &req) {
		return
	}
	if errMsg := validateChannelKey(req); errMsg != "" {
		writeJSONError(c, errMsg)
		return
	}
	if req.ChannelType == frame.ChannelTypePerson {
		writeJSONError(c, "个人频道不支持此操作！")
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.RemoveAllSubscribers(c.Request.Context(), req.toKey()))
}

func (s *Server) handleTmpChannelSubscriberSet(c *gin.Context) {
	var req tmpChannelSubscriberRequest
	if !bindJSON(c, &req) {
		return
	}
	if errMsg := validateTmpSubscriberSet(req); errMsg != "" {
		writeJSONError(c, errMsg)
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.SetTempSubscribers(c.Request.Context(), channelusecase.TempSubscriberCommand{
		ChannelID: req.ChannelID,
		UIDs:      req.UIDs,
	}))
}

func (s *Server) handleChannelDenylistAdd(c *gin.Context) {
	var req channelMemberRequest
	if !bindChannelMember(c, &req, true) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.AddDenylist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelDenylistSet(c *gin.Context) {
	var req channelMemberRequest
	if !bindChannelMemberSet(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.SetDenylist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelDenylistRemove(c *gin.Context) {
	var req channelMemberRequest
	if !bindChannelMember(c, &req, false) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.RemoveDenylist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelDenylistRemoveAll(c *gin.Context) {
	var req channelKeyRequest
	if !bindChannelKey(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.RemoveAllDenylist(c.Request.Context(), req.toKey()))
}

func (s *Server) handleChannelAllowlistAdd(c *gin.Context) {
	var req channelMemberRequest
	if !bindAllowMember(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.AddAllowlist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelAllowlistSet(c *gin.Context) {
	var req channelMemberRequest
	if !bindChannelMemberSet(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.SetAllowlist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelAllowlistRemove(c *gin.Context) {
	var req channelMemberRequest
	if !bindAllowMember(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.RemoveAllowlist(c.Request.Context(), req.toMemberCommand()))
}

func (s *Server) handleChannelAllowlistRemoveAll(c *gin.Context) {
	var req channelKeyRequest
	if !bindChannelKey(c, &req) {
		return
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	writeMutationResult(c, s.channels.RemoveAllAllowlist(c.Request.Context(), req.toKey()))
}

func (s *Server) handleChannelAllowlistGet(c *gin.Context) {
	channelType, _ := strconv.ParseUint(c.Query("channel_type"), 10, 8)
	key := channelusecase.ChannelKey{
		ChannelID:   c.Query("channel_id"),
		ChannelType: uint8(channelType),
	}
	if err := s.requireChannelUsecase(); err != nil {
		writeJSONError(c, err.Error())
		return
	}
	result, err := s.channels.ListAllowlist(c.Request.Context(), key)
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, result.Members)
}

func bindJSON(c *gin.Context, out any) bool {
	if err := c.ShouldBindJSON(out); err != nil {
		writeJSONError(c, "数据格式有误！")
		return false
	}
	return true
}

func bindChannelMember(c *gin.Context, req *channelMemberRequest, requireUIDs bool) bool {
	if !bindJSON(c, req) {
		return false
	}
	if errMsg := validateChannelMember(*req, requireUIDs); errMsg != "" {
		writeJSONError(c, errMsg)
		return false
	}
	return true
}

func bindChannelMemberSet(c *gin.Context, req *channelMemberRequest) bool {
	if !bindJSON(c, req) {
		return false
	}
	if strings.TrimSpace(req.ChannelID) == "" {
		writeJSONError(c, "频道ID不能为空！")
		return false
	}
	return true
}

func bindAllowMember(c *gin.Context, req *channelMemberRequest) bool {
	if !bindJSON(c, req) {
		return false
	}
	if errMsg := validateAllowMember(*req); errMsg != "" {
		writeJSONError(c, errMsg)
		return false
	}
	return true
}

func bindChannelKey(c *gin.Context, req *channelKeyRequest) bool {
	if !bindJSON(c, req) {
		return false
	}
	if errMsg := validateChannelKey(*req); errMsg != "" {
		writeJSONError(c, errMsg)
		return false
	}
	return true
}

func (s *Server) requireChannelUsecase() error {
	if s == nil || s.channels == nil {
		return errChannelUsecaseRequired
	}
	return nil
}

var errChannelUsecaseRequired = errors.New("channel usecase not configured")

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
	if containsSpecialChar(req.ChannelID) {
		return "频道ID不能包含特殊字符！"
	}
	return ""
}

func validateSubscriberChange(req channelSubscriberRequest) string {
	if strings.TrimSpace(req.ChannelID) == "" {
		return "频道ID不能为空！"
	}
	if containsSpecialChar(req.ChannelID) {
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
	if containsSpecialChar(req.ChannelID) {
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
	if containsSpecialChar(req.ChannelID) {
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

func containsSpecialChar(value string) bool {
	return strings.Contains(value, "#") || strings.Contains(value, "@")
}

func stringArrayIsEmpty(values []string) bool {
	if len(values) == 0 {
		return true
	}
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func writeJSONError(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"msg":    message,
		"status": http.StatusBadRequest,
	})
}

func writeMutationResult(c *gin.Context, err error) {
	if err != nil {
		writeJSONError(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": http.StatusOK})
}
