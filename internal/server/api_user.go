package server

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// UserAPI 用户相关API
type UserAPI struct {
	wklog.Log
	s *Server
}

// NewUserAPI NewUserAPI
func NewUserAPI(s *Server) *UserAPI {
	return &UserAPI{
		Log: wklog.NewWKLog("UserAPI"),
		s:   s,
	}
}

// Route 用户相关路由配置
func (u *UserAPI) Route(r *wkhttp.WKHttp) {

	r.POST("/user/token", u.updateToken)                  // 更新用户token
	r.POST("/user/device_quit", u.deviceQuit)             // 强制设备退出
	r.POST("/user/onlinestatus", u.getOnlineStatus)       // 获取用户在线状态
	r.POST("/user/systemuids_add", u.systemUIDsAdd)       // 添加系统uid
	r.POST("/user/systemuids_remove", u.systemUIDsRemove) // 移除系统uid

}

// 强制设备退出
func (u *UserAPI) deviceQuit(c *wkhttp.Context) {
	var req struct {
		UID        string `json:"uid"`         // 用户uid
		DeviceFlag int    `json:"device_flag"` // 设备flag 这里 -1 为用户所有的设备
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if u.s.opts.ClusterOn() {
		leaderInfo, err := u.s.cluster.LeaderNodeOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			u.Error("获取频道所在节点失败！", zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.NodeID == u.s.opts.Cluster.PeerID
		if !leaderIsSelf {
			u.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	if req.DeviceFlag == -1 {
		_ = u.quitUserDevice(req.UID, wkproto.APP)
		_ = u.quitUserDevice(req.UID, wkproto.WEB)
		_ = u.quitUserDevice(req.UID, wkproto.PC)
	} else {
		_ = u.quitUserDevice(req.UID, wkproto.DeviceFlag(req.DeviceFlag))
	}

	c.ResponseOK()

}

// 这里清空token 让设备去重新登录 空token是不让登录的
func (u *UserAPI) quitUserDevice(uid string, deviceFlag wkproto.DeviceFlag) error {
	err := u.s.store.UpdateUserToken(uid, deviceFlag.ToUint8(), uint8(wkproto.DeviceLevelMaster), "") // 这里的deviceLevel可以随便给 不影响逻辑 这里随便给的master
	if err != nil {
		u.Error("清空用户token失败！", zap.Error(err), zap.String("uid", uid), zap.Uint8("deviceFlag", deviceFlag.ToUint8()))
		return err
	}
	oldConns := u.s.connManager.GetConnsWith(uid, deviceFlag)
	if len(oldConns) > 0 {
		for _, oldConn := range oldConns {
			u.s.dispatch.dataOutFrames(oldConn, &wkproto.DisconnectPacket{
				ReasonCode: wkproto.ReasonConnectKick,
			})
			u.s.timingWheel.AfterFunc(time.Second*2, func() {
				oldConn.Close()
			})
		}
	}

	return nil
}

func (u *UserAPI) getOnlineStatus(c *wkhttp.Context) {
	var uids []string
	err := c.BindJSON(&uids)
	if err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if len(uids) == 0 {
		c.ResponseOK()
		return
	}

	var conns []*OnlinestatusResp
	if u.s.opts.ClusterOn() {
		var err error
		conns, err = u.getOnlineConnsForCluster(uids)
		if err != nil {
			u.Error("获取在线状态失败！", zap.Error(err))
			c.ResponseError(err)
			return
		}
	} else {
		conns = u.getOnlineConns(uids)
	}

	c.JSON(http.StatusOK, conns)
}

func (u *UserAPI) getOnlineConnsForCluster(uids []string) ([]*OnlinestatusResp, error) {
	uidInPeerMap := make(map[uint64][]string)
	localUids := make([]string, 0)
	for _, uid := range uids {
		leaderInfo, err := u.s.cluster.LeaderNodeOfChannel(uid, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			u.Error("获取频道所在节点失败！", zap.String("channelID", uid), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			return nil, errors.New("获取频道所在节点失败！")
		}
		leaderIsSelf := leaderInfo.NodeID == u.s.opts.Cluster.PeerID
		if leaderIsSelf {
			localUids = append(localUids, uid)
			continue
		}
		uidList := uidInPeerMap[leaderInfo.NodeID]
		if uidList == nil {
			uidList = make([]string, 0)
		}
		uidList = append(uidList, uid)
		uidInPeerMap[leaderInfo.NodeID] = uidList
	}
	var conns []*OnlinestatusResp
	if len(localUids) > 0 {
		conns = u.getOnlineConns(localUids)
	}
	if len(uidInPeerMap) > 0 {
		var reqErr error
		wg := &sync.WaitGroup{}
		for peerID, uidList := range uidInPeerMap {
			wg.Add(1)
			go func(pid uint64, uidArr []string) {
				results, err := u.requestOnlineStatus(pid, uidArr)
				if err != nil {
					reqErr = err
				} else {
					conns = append(conns, results...)
				}
				wg.Done()
			}(peerID, uidList)
		}
		wg.Wait()
		if reqErr != nil {
			return nil, reqErr
		}
	}

	return conns, nil
}

func (u *UserAPI) requestOnlineStatus(nodeID uint64, uids []string) ([]*OnlinestatusResp, error) {

	nodeInfo, err := u.s.cluster.NodeInfoByID(nodeID) // 获取频道的领导节点
	if err != nil {
		u.Error("获取频道所在节点失败！", zap.Uint64("nodeID", nodeID))
		return nil, errors.New("获取频道所在节点失败！")
	}
	reqURL := fmt.Sprintf("%s/user/onlinestatus", nodeInfo.ApiServerAddr)
	resp, err := network.Post(reqURL, []byte(wkutil.ToJSON(uids)), nil)
	if err != nil {
		u.Error("获取在线用户状态失败！", zap.Error(err), zap.String("reqURL", reqURL))
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("获取在线用户状态请求状态错误！[%d]", resp.StatusCode)
	}
	var onlineStatusResps []*OnlinestatusResp
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &onlineStatusResps)
	if err != nil {
		u.Error("解析在线用户uids失败！", zap.Error(err))
		return nil, err
	}
	return onlineStatusResps, nil
}

func (u *UserAPI) getOnlineConns(uids []string) []*OnlinestatusResp {
	conns := u.s.connManager.GetOnlineConns(uids)

	onlineStatusResps := make([]*OnlinestatusResp, 0, len(conns))
	for _, conn := range conns {
		onlineStatusResps = append(onlineStatusResps, &OnlinestatusResp{
			UID:        conn.UID(),
			DeviceFlag: conn.DeviceFlag(),
			Online:     1,
		})
	}
	return onlineStatusResps
}

// 更新用户的token
func (u *UserAPI) updateToken(c *wkhttp.Context) {
	var req UpdateTokenReq
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}
	if err := req.Check(); err != nil {
		c.ResponseError(err)
		return
	}

	if u.s.opts.ClusterOn() {
		leaderInfo, err := u.s.cluster.LeaderNodeOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			u.Error("获取频道所在节点失败！", zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			c.ResponseError(errors.New("获取频道所在节点失败！"))
			return
		}
		leaderIsSelf := leaderInfo.NodeID == u.s.opts.Cluster.PeerID
		if !leaderIsSelf {
			u.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
			c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
			return
		}
	}

	u.Debug("req", zap.Any("req", req))

	ban := false // 是否被封禁

	channelInfo, err := u.s.store.GetChannel(req.UID, wkproto.ChannelTypePerson)
	if err != nil {
		u.Error("获取频道信息失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(err)
		return
	}
	if channelInfo != nil {
		ban = channelInfo.Ban
	}
	if ban {
		c.ResponseStatus(int(wkproto.ReasonBan))
		return
	}

	err = u.s.store.UpdateUserToken(req.UID, req.DeviceFlag.ToUint8(), uint8(req.DeviceLevel), req.Token)
	if err != nil {
		u.Error("更新用户token失败！", zap.Error(err))
		c.ResponseError(errors.Wrap(err, "更新用户token失败！"))
		return
	}

	if req.DeviceLevel == wkproto.DeviceLevelMaster {
		// 如果存在旧连接，则发起踢出请求
		oldConns := u.s.connManager.GetConnsWith(req.UID, req.DeviceFlag)
		if len(oldConns) > 0 {
			for _, oldConn := range oldConns {
				u.Debug("更新Token时，存在旧连接！", zap.String("uid", req.UID), zap.Int64("id", oldConn.ID()), zap.String("deviceFlag", req.DeviceFlag.String()))
				u.s.dispatch.dataOutFrames(oldConn, &wkproto.DisconnectPacket{
					ReasonCode: wkproto.ReasonConnectKick,
					Reason:     "账号在其他设备上登录",
				})
				u.s.timingWheel.AfterFunc(time.Second*10, func() {
					oldConn.Close()
				})
			}

		}
	}

	// // 创建或更新个人频道
	// err = u.s.channelManager.CreateOrUpdatePersonChannel(req.UID)
	// if err != nil {
	// 	u.Error("创建个人频道失败！", zap.Error(err))
	// 	c.ResponseError(errors.New("创建个人频道失败！"))
	// 	return
	// }
	c.ResponseOK()
}

// 添加系统uid
func (u *UserAPI) systemUIDsAdd(c *wkhttp.Context) {
	var req struct {
		UIDs []string `json:"uids"`
	}
	if err := c.BindJSON(&req); err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}
	if u.s.opts.ClusterOn() {
		c.ResponseError(errors.New("分布式情况下暂不支持！"))
		return
	}
	if len(req.UIDs) > 0 {
		err := u.s.systemUIDManager.AddSystemUIDs(req.UIDs)
		if err != nil {
			u.Error("添加系统账号失败！", zap.Error(err))
			c.ResponseError(errors.New("添加系统账号失败！"))
			return
		}
	}
	c.ResponseOK()

}

// 移除系统uid
func (u *UserAPI) systemUIDsRemove(c *wkhttp.Context) {
	var req struct {
		UIDs []string `json:"uids"`
	}
	if err := c.BindJSON(&req); err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(errors.New("数据格式有误！"))
		return
	}
	if u.s.opts.ClusterOn() {
		c.ResponseError(errors.New("分布式情况下暂不支持！"))
		return
	}
	if len(req.UIDs) > 0 {
		err := u.s.systemUIDManager.RemoveSystemUIDs(req.UIDs)
		if err != nil {
			u.Error("移除系统账号失败！", zap.Error(err))
			c.ResponseError(errors.New("移除系统账号失败！"))
			return
		}
	}
	c.ResponseOK()
}

// UpdateTokenReq 更新token请求
type UpdateTokenReq struct {
	UID         string              `json:"uid"`          // 用户唯一uid
	Token       string              `json:"token"`        // 用户的token
	DeviceFlag  wkproto.DeviceFlag  `json:"device_flag"`  // 设备标识  0.app 1.web
	DeviceLevel wkproto.DeviceLevel `json:"device_level"` // 设备等级 0.为从设备 1.为主设备
}

// Check 检查输入
func (u UpdateTokenReq) Check() error {
	if u.UID == "" {
		return errors.New("uid不能为空！")
	}
	// if len(u.UID) > 32 {
	// 	return errors.New("uid不能大于32位")
	// }
	if u.Token == "" {
		return errors.New("token不能为空！")
	}
	// if len(u.PublicKey) <= 0 {
	// 	return errors.New("用户RSA公钥不能为空！")
	// }
	// if len(u.Token) > 32 {
	// 	return errors.New("token不能大于32位")
	// }
	return nil
}

type OnlinestatusResp struct {
	UID        string `json:"uid"`         // 在线用户uid
	DeviceFlag uint8  `json:"device_flag"` // 设备标记 0. APP 1.web
	Online     int    `json:"online"`      // 是否在线
}
