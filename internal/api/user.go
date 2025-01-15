package api

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/cluster/node/types"
	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
	"github.com/pkg/errors"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

// user 用户相关API
type user struct {
	wklog.Log
	s *Server
}

func newUser(s *Server) *user {
	return &user{
		Log: wklog.NewWKLog("user"),
		s:   s,
	}
}

// Route 用户相关路由配置
func (u *user) route(r *wkhttp.WKHttp) {

	r.POST("/user/token", u.updateToken)                  // 更新用户token
	r.POST("/user/device_quit", u.deviceQuit)             // 强制设备退出
	r.POST("/user/onlinestatus", u.getOnlineStatus)       // 获取用户在线状态
	r.POST("/user/systemuids_add", u.systemUidsAdd)       // 添加系统uid
	r.POST("/user/systemuids_remove", u.systemUidsRemove) // 移除系统uid
	r.GET("/user/systemuids", u.getSystemUids)            // 获取系统uid

	r.POST("/user/systemuids_add_to_cache", u.systemUidsAddToCache)           // 仅仅添加系统账号至缓存
	r.POST("/user/systemuids_remove_from_cache", u.systemUidsRemoveFromCache) // 仅仅从缓存中移除系统账号

}

// 强制设备退出
func (u *user) deviceQuit(c *wkhttp.Context) {
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
	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		u.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		u.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
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
func (u *user) quitUserDevice(uid string, deviceFlag wkproto.DeviceFlag) error {

	device, err := service.Store.GetDevice(uid, deviceFlag)
	if err != nil {
		u.Error("获取设备信息失败！", zap.Error(err), zap.String("uid", uid), zap.Uint8("deviceFlag", deviceFlag.ToUint8()))
		return err
	}
	if wkdb.IsEmptyDevice(device) {
		u.Error("设备信息不存在！", zap.String("uid", uid), zap.Uint8("deviceFlag", deviceFlag.ToUint8()))
		return errors.New("设备信息不存在！")
	}

	updatedAt := time.Now()
	err = service.Store.UpdateDevice(wkdb.Device{
		Id:          device.Id,
		Uid:         uid,
		DeviceFlag:  uint64(deviceFlag),
		DeviceLevel: uint8(wkproto.DeviceLevelMaster),
		Token:       "",
		CreatedAt:   device.CreatedAt,
		UpdatedAt:   &updatedAt,
	}) // 这里的deviceLevel可以随便给 不影响逻辑 这里随便给的master
	if err != nil {
		u.Error("清空用户token失败！", zap.Error(err), zap.String("uid", uid), zap.Uint8("deviceFlag", deviceFlag.ToUint8()))
		return err
	}
	oldConns := eventbus.User.ConnsByDeviceFlag(uid, deviceFlag)
	if len(oldConns) > 0 {
		for _, oldConn := range oldConns {
			eventbus.User.ConnWrite(oldConn, &wkproto.DisconnectPacket{
				ReasonCode: wkproto.ReasonConnectKick,
			})
			u.s.timingWheel.AfterFunc(time.Second*2, func() {
				eventbus.User.CloseConn(oldConn)
			})
		}
	}

	return nil
}

func (u *user) getOnlineStatus(c *wkhttp.Context) {
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
	conns, err = u.getOnlineConnsForCluster(uids)
	if err != nil {
		u.Error("获取在线状态失败！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, conns)
}

func (u *user) getOnlineConnsForCluster(uids []string) ([]*OnlinestatusResp, error) {
	uidInPeerMap := make(map[uint64][]string)
	localUids := make([]string, 0)
	for _, uid := range uids {
		leaderInfo, err := service.Cluster.SlotLeaderOfChannel(uid, wkproto.ChannelTypePerson) // 获取频道的领导节点
		if err != nil {
			u.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", uid), zap.Uint8("channelType", wkproto.ChannelTypePerson))
			return nil, errors.New("获取频道所在节点失败！")
		}
		leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
		if leaderIsSelf {
			localUids = append(localUids, uid)
			continue
		}
		uidList := uidInPeerMap[leaderInfo.Id]
		if uidList == nil {
			uidList = make([]string, 0)
		}
		uidList = append(uidList, uid)
		uidInPeerMap[leaderInfo.Id] = uidList
	}
	var conns []*OnlinestatusResp
	if len(localUids) > 0 {
		conns = u.getOnlineConns(localUids)
	}
	if len(uidInPeerMap) > 0 {
		var reqErr error
		wg := &sync.WaitGroup{}
		for nodeId, uidList := range uidInPeerMap {
			wg.Add(1)
			go func(pid uint64, uidArr []string) {
				results, err := u.requestOnlineStatus(pid, uidArr)
				if err != nil {
					reqErr = err
				} else {
					conns = append(conns, results...)
				}
				wg.Done()
			}(nodeId, uidList)
		}
		wg.Wait()
		if reqErr != nil {
			return nil, reqErr
		}
	}

	return conns, nil
}

func (u *user) requestOnlineStatus(nodeId uint64, uids []string) ([]*OnlinestatusResp, error) {

	nodeInfo := service.Cluster.NodeInfoById(nodeId) // 获取频道的领导节点
	if nodeInfo == nil {
		u.Error("节点信息不存在", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("节点信息不存在")
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

func (u *user) getOnlineConns(uids []string) []*OnlinestatusResp {

	onlineStatusResps := make([]*OnlinestatusResp, 0)
	for _, uid := range uids {
		conns := eventbus.User.ConnsByUid(uid)
		for _, conn := range conns {
			onlineStatusResps = append(onlineStatusResps, &OnlinestatusResp{
				UID:        conn.Uid,
				DeviceFlag: conn.DeviceFlag.ToUint8(),
				Online:     1,
			})
		}
	}
	return onlineStatusResps
}

// 更新用户的token
func (u *user) updateToken(c *wkhttp.Context) {
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

	if req.UID == options.G.SystemUID {
		c.ResponseError(errors.New("系统账号不允许更新token！"))
		return
	}

	leaderInfo, err := service.Cluster.SlotLeaderOfChannel(req.UID, wkproto.ChannelTypePerson) // 获取频道的领导节点
	if err != nil {
		u.Error("获取频道所在节点失败！", zap.Error(err), zap.String("channelID", req.UID), zap.Uint8("channelType", wkproto.ChannelTypePerson))
		c.ResponseError(errors.New("获取频道所在节点失败！"))
		return
	}
	leaderIsSelf := leaderInfo.Id == options.G.Cluster.NodeId
	if !leaderIsSelf {
		u.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", leaderInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	u.Debug("req", zap.Any("req", req))

	ban := false // 是否被封禁

	channelInfo, err := service.Store.GetChannel(req.UID, wkproto.ChannelTypePerson)
	if err != nil {
		u.Error("获取频道信息失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(err)
		return
	}
	if !wkdb.IsEmptyChannelInfo(channelInfo) {
		ban = channelInfo.Ban
	}
	if ban {
		c.ResponseStatus(int(wkproto.ReasonBan))
		return
	}

	user, err := service.Store.GetUser(req.UID) // 确保用户存在
	if err != nil && err != wkdb.ErrNotFound {
		u.Error("获取用户信息失败！", zap.Error(err), zap.String("uid", req.UID))
		c.ResponseError(err)
		return
	}

	// 如果用户不存在，则添加用户
	if wkdb.IsEmptyUser(user) {
		createdAt := time.Now()
		updatedAt := time.Now()
		err = service.Store.AddUser(wkdb.User{
			Uid:       req.UID,
			CreatedAt: &createdAt,
			UpdatedAt: &updatedAt,
		})
		if err != nil {
			u.Error("添加用户失败！", zap.Error(err), zap.String("uid", req.UID))
			c.ResponseError(err)
			return
		}
	}

	device, err := service.Store.GetDevice(req.UID, req.DeviceFlag)
	if err != nil && err != wkdb.ErrNotFound {
		u.Error("获取设备信息失败！", zap.Error(err), zap.String("uid", req.UID), zap.Uint8("deviceFlag", req.DeviceFlag.ToUint8()))
		c.ResponseError(err)
		return
	}

	// 不存在设备则添加设备，存在则更新设备
	if wkdb.IsEmptyDevice(device) {
		createdAt := time.Now()
		err = service.Store.AddDevice(wkdb.Device{
			Id:          service.Store.NextPrimaryKey(),
			Uid:         req.UID,
			DeviceFlag:  uint64(req.DeviceFlag),
			DeviceLevel: uint8(req.DeviceLevel),
			Token:       req.Token,
			CreatedAt:   &createdAt,
			UpdatedAt:   &createdAt,
		})
		if err != nil {
			u.Error("添加设备失败！", zap.Error(err), zap.String("uid", req.UID), zap.Uint8("deviceFlag", req.DeviceFlag.ToUint8()))
			c.ResponseError(err)
			return
		}
	} else {
		updatedAt := time.Now()
		err = service.Store.UpdateDevice(wkdb.Device{
			Id:          device.Id,
			Uid:         req.UID,
			DeviceFlag:  uint64(req.DeviceFlag),
			DeviceLevel: uint8(req.DeviceLevel),
			Token:       req.Token,
			UpdatedAt:   &updatedAt,
		})
		if err != nil {
			u.Error("更新设备失败！", zap.Error(err), zap.String("uid", req.UID), zap.Uint8("deviceFlag", req.DeviceFlag.ToUint8()))
			c.ResponseError(err)
			return
		}
	}

	if req.DeviceLevel == wkproto.DeviceLevelMaster {
		// 如果存在旧连接，则发起踢出请求
		oldConns := eventbus.User.ConnsByDeviceFlag(req.UID, req.DeviceFlag)
		if len(oldConns) > 0 {
			for _, oldConn := range oldConns {
				u.Debug("更新Token时，存在旧连接！", zap.String("uid", req.UID), zap.Int64("id", oldConn.ConnId), zap.String("deviceFlag", req.DeviceFlag.String()))
				// 踢旧连接
				eventbus.User.ConnWrite(oldConn, &wkproto.DisconnectPacket{
					ReasonCode: wkproto.ReasonConnectKick,
					Reason:     "账号在其他设备上登录",
				})
				u.s.timingWheel.AfterFunc(time.Second*10, func() {
					eventbus.User.CloseConn(oldConn)
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
func (u *user) systemUidsAdd(c *wkhttp.Context) {
	var req struct {
		UIDs []string `json:"uids"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	var slotId uint32 = 0 // 系统uid默认存储在slot 0上

	nodeInfo := service.Cluster.SlotLeaderNodeInfo(slotId)
	if nodeInfo == nil {
		u.Error("槽的领导节点不存在", zap.Error(err), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("槽的领导节点不存在"))
		return
	}
	if nodeInfo.Id != options.G.Cluster.NodeId {
		u.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	// 将系统账号存储下来
	if len(req.UIDs) > 0 {
		err := service.SystemAccountManager.AddSystemUids(req.UIDs)
		if err != nil {
			u.Error("添加系统账号失败！", zap.Error(err))
			c.ResponseError(errors.New("添加系统账号失败！"))
			return
		}
	}

	// 将系统账号添加到各个节点的缓存内
	nodes := service.Cluster.Nodes()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), options.G.Cluster.ReqTimeout)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, node := range nodes {
		if node.Id == options.G.Cluster.NodeId {
			continue
		}
		if !node.Online {
			continue
		}
		requestGroup.Go(func(n *types.Node) func() error {
			return func() error {
				return u.requestSystemUidsAddToCache(n, req.UIDs)
			}
		}(node))
	}

	err = requestGroup.Wait()
	if err != nil {
		u.Error("添加系统账号到缓存失败！", zap.Error(err))
		c.ResponseError(errors.New("添加系统账号到缓存失败！"))
		return
	}

	c.ResponseOK()

}

func (u *user) requestSystemUidsAddToCache(nodeInfo *types.Node, uids []string) error {
	reqURL := fmt.Sprintf("%s/user/systemuids_add_to_cache", nodeInfo.ApiServerAddr)
	resp, err := network.Post(reqURL, []byte(wkutil.ToJSON(map[string]interface{}{
		"uids": uids,
	})), nil)
	if err != nil {
		u.Error("添加系统账号到缓存失败！", zap.Error(err), zap.String("reqURL", reqURL))
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("添加系统账号到缓存请求状态错误！[%d]", resp.StatusCode)
	}
	return nil
}

func (u *user) systemUidsAddToCache(c *wkhttp.Context) {
	var req struct {
		UIDs []string `json:"uids"`
	}
	if err := c.BindJSON(&req); err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if len(req.UIDs) > 0 {
		service.SystemAccountManager.AddSystemUidsToCache(req.UIDs)
	}
	c.ResponseOK()
}

// 移除系统uid
func (u *user) systemUidsRemove(c *wkhttp.Context) {
	var req struct {
		UIDs []string `json:"uids"`
	}
	bodyBytes, err := BindJSON(&req, c)
	if err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	var slotId uint32 = 0 // 系统uid默认存储在slot 0上
	nodeInfo := service.Cluster.SlotLeaderNodeInfo(slotId)
	if nodeInfo == nil {
		u.Error("获取slot所在节点失败！", zap.Error(err), zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("获取slot所在节点失败！"))
		return
	}
	if nodeInfo.Id != options.G.Cluster.NodeId {
		u.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path)))
		c.ForwardWithBody(fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path), bodyBytes)
		return
	}

	if len(req.UIDs) > 0 {
		err := service.SystemAccountManager.RemoveSystemUids(req.UIDs)
		if err != nil {
			u.Error("移除系统账号失败！", zap.Error(err))
			c.ResponseError(errors.New("移除系统账号失败！"))
			return
		}
	}

	// 将系统账号从各个节点的缓存内移除
	nodes := service.Cluster.Nodes()

	timeoutCtx, cancel := context.WithTimeout(context.Background(), options.G.Cluster.ReqTimeout)
	defer cancel()
	requestGroup, _ := errgroup.WithContext(timeoutCtx)
	for _, node := range nodes {
		if node.Id == options.G.Cluster.NodeId {
			continue
		}
		if !node.Online {
			continue
		}
		requestGroup.Go(func(n *types.Node) func() error {
			return func() error {
				return u.requestSystemUidsRemoveFromCache(n, req.UIDs)
			}
		}(node))
	}

	err = requestGroup.Wait()
	if err != nil {
		u.Error("移除系统账号从缓存失败！", zap.Error(err))
		c.ResponseError(errors.New("移除系统账号从缓存失败！"))
		return
	}

	c.ResponseOK()
}

func (u *user) requestSystemUidsRemoveFromCache(nodeInfo *types.Node, uids []string) error {
	reqURL := fmt.Sprintf("%s/user/systemuids_remove_from_cache", nodeInfo.ApiServerAddr)
	resp, err := network.Post(reqURL, []byte(wkutil.ToJSON(map[string]interface{}{
		"uids": uids,
	})), nil)
	if err != nil {
		u.Error("移除系统账号从缓存失败！", zap.Error(err), zap.String("reqURL", reqURL))
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("移除系统账号从缓存请求状态错误！[%d]", resp.StatusCode)
	}
	return nil
}

func (u *user) systemUidsRemoveFromCache(c *wkhttp.Context) {
	var req struct {
		UIDs []string `json:"uids"`
	}
	if err := c.BindJSON(&req); err != nil {
		u.Error("数据格式有误！", zap.Error(err))
		c.ResponseError(err)
		return
	}

	if len(req.UIDs) > 0 {
		service.SystemAccountManager.RemoveSystemUidsFromCache(req.UIDs)
	}
	c.ResponseOK()
}

func (u *user) getSystemUids(c *wkhttp.Context) {

	var slotId uint32 = 0 // 系统uid默认存储在slot 0上
	nodeInfo := service.Cluster.SlotLeaderNodeInfo(slotId)
	if nodeInfo == nil {
		u.Error("获取slot所在节点失败！", zap.Uint32("slotId", slotId))
		c.ResponseError(errors.New("获取slot所在节点失败！"))
		return
	}
	if nodeInfo.Id != options.G.Cluster.NodeId {
		u.Debug("转发请求：", zap.String("url", fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path)))
		c.Forward(fmt.Sprintf("%s%s", nodeInfo.ApiServerAddr, c.Request.URL.Path))
		return
	}

	uids, err := service.Store.GetSystemUids()
	if err != nil {
		u.Error("获取系统账号失败！", zap.Error(err))
		c.ResponseError(errors.New("获取系统账号失败！"))
		return
	}

	c.JSON(http.StatusOK, uids)
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

	if options.IsSpecialChar(u.UID) {
		return errors.New("uid不能包含特殊字符！")
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
