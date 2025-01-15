package cluster

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/WuKongIM/WuKongIM/pkg/network"
	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wkhttp"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

func (s *Server) userSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	uid := strings.TrimSpace(c.Query("uid"))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	pre := wkutil.ParseInt(c.Query("pre")) // 是否向前搜索
	offsetCreatedAt := wkutil.ParseInt64(c.Query("offset_created_at"))

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var searchLocalUsers = func() (userRespTotal, error) {
		users, err := s.db.SearchUser(wkdb.UserSearchReq{
			Uid:             uid,
			Limit:           limit + 1, // 实际查询出来的数据比limit多1，用于判断是否有下一页
			OffsetCreatedAt: offsetCreatedAt,
			Pre:             pre == 1,
		})
		if err != nil {
			s.Error("search user failed", zap.Error(err))
			return userRespTotal{}, err
		}

		userResps := make([]*userResp, 0, len(users))
		for _, user := range users {
			userResp := newUserResp(user)
			slot := s.getSlotId(user.Uid)
			userResp.Slot = slot
			userResps = append(userResps, userResp)

		}

		count, err := s.db.GetTotalUserCount()
		if err != nil {
			s.Error("GetTotalUserCount error", zap.Error(err))
			return userRespTotal{}, err
		}
		return userRespTotal{
			Data:  userResps,
			Total: count,
		}, nil
	}

	if nodeId == s.opts.ConfigOptions.NodeId {
		result, err := searchLocalUsers()
		if err != nil {
			s.Error("search local users  failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	nodes := s.cfgServer.Nodes()
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	userResps := make([]*userResp, 0)
	for _, node := range nodes {

		if node.Id == s.opts.ConfigOptions.NodeId {
			result, err := searchLocalUsers()
			if err != nil {
				s.Error("search local users  failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			userResps = append(userResps, result.Data...)
			continue
		}

		if !s.NodeIsOnline(node.Id) {
			continue
		}
		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestUserSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				userResps = append(userResps, result.Data...)
				return nil
			}
		}(node.Id, c.Request.URL.Query()))

	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("search user request failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 移除userResps中重复的数据
	userRespMap := make(map[string]*userResp)
	for _, userResp := range userResps {
		userRespMap[userResp.Uid] = userResp
	}

	userResps = make([]*userResp, 0, len(userRespMap))
	for _, userResp := range userRespMap {
		userResps = append(userResps, userResp)
	}

	sort.Slice(userResps, func(i, j int) bool {
		return uint64(userResps[i].CreatedAt) > uint64(userResps[j].CreatedAt)
	})

	hasMore := false
	if len(userResps) > limit {
		hasMore = true
		if pre == 1 {
			userResps = userResps[len(userResps)-limit:]
		} else {
			userResps = userResps[:limit]
		}
	}

	userCount, err := s.db.GetTotalUserCount()
	if err != nil {
		s.Error("GetTotalUserCount error", zap.Error(err))
		c.ResponseError(err)
		return
	}

	c.JSON(http.StatusOK, userRespTotal{
		Total: userCount,
		More:  wkutil.BoolToInt(hasMore),
		Data:  userResps,
	})
}

func (s *Server) requestUserSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*userRespTotal, error) {
	node := s.cfgServer.Node(nodeId)
	if node == nil {
		s.Error("requestUserSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var userRespTotal *userRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &userRespTotal)
	if err != nil {
		return nil, err
	}
	return userRespTotal, nil
}

func (s *Server) deviceSearch(c *wkhttp.Context) {
	// 搜索条件
	limit := wkutil.ParseInt(c.Query("limit"))
	uid := strings.TrimSpace(c.Query("uid"))
	deviceFlag := wkutil.ParseUint64(strings.TrimSpace(c.Query("device_flag")))
	nodeId := wkutil.ParseUint64(c.Query("node_id"))
	pre := wkutil.ParseInt(c.Query("pre"))                             // 是否向前搜索
	offsetCreatedAt := wkutil.ParseInt64(c.Query("offset_created_at")) // 偏移的创建时间

	if limit <= 0 {
		limit = s.opts.PageSize
	}

	var searchLocalDevice = func() (*deviceRespTotal, error) {
		devices, err := s.db.SearchDevice(wkdb.DeviceSearchReq{
			Uid:             uid,
			DeviceFlag:      deviceFlag,
			Limit:           limit + 1, // 实际查询出来的数据比limit多1，用于判断是否有下一页
			Pre:             pre == 1,
			OffsetCreatedAt: offsetCreatedAt,
		})
		if err != nil {
			s.Error("search device failed", zap.Error(err))
			return nil, err
		}
		deviceResps := make([]*deviceResp, 0, len(devices))
		for _, device := range devices {
			deviceResps = append(deviceResps, newDeviceResp(device))
		}
		count, err := s.db.GetTotalDeviceCount()
		if err != nil {
			s.Error("GetTotalDeviceCount error", zap.Error(err))
			return nil, err
		}

		return &deviceRespTotal{
			Total: count,
			Data:  deviceResps,
		}, nil

	}

	if nodeId == s.opts.ConfigOptions.NodeId {
		result, err := searchLocalDevice()
		if err != nil {
			s.Error("search local device  failed", zap.Error(err))
			c.ResponseError(err)
			return
		}
		c.JSON(http.StatusOK, result)
		return
	}

	nodes := s.cfgServer.Nodes()
	timeoutCtx, cancel := context.WithTimeout(s.cancelCtx, s.opts.ReqTimeout)
	defer cancel()

	requestGroup, _ := errgroup.WithContext(timeoutCtx)

	deviceResps := make([]*deviceResp, 0)
	for _, node := range nodes {
		if node.Id == s.opts.ConfigOptions.NodeId {
			result, err := searchLocalDevice()
			if err != nil {
				s.Error("search local users  failed", zap.Error(err))
				c.ResponseError(err)
				return
			}
			deviceResps = append(deviceResps, result.Data...)
			continue
		}

		if !s.NodeIsOnline(node.Id) {
			continue
		}

		requestGroup.Go(func(nId uint64, queryValues url.Values) func() error {
			return func() error {
				queryMap := map[string]string{}
				for key, values := range queryValues {
					if len(values) > 0 {
						queryMap[key] = values[0]
					}
				}
				result, err := s.requestDeviceSearch(c.Request.URL.Path, nId, queryMap, c.CopyRequestHeader(c.Request))
				if err != nil {
					return err
				}
				deviceResps = append(deviceResps, result.Data...)
				return nil
			}
		}(node.Id, c.Request.URL.Query()))

	}

	err := requestGroup.Wait()
	if err != nil {
		s.Error("search device request failed", zap.Error(err))
		c.ResponseError(err)
		return
	}

	// 移除deviceResps中重复的数据
	deviceRespMap := make(map[string]*deviceResp)
	for _, deviceResp := range deviceResps {
		deviceRespMap[fmt.Sprintf("%s-%d", deviceResp.Uid, deviceResp.DeviceFlag)] = deviceResp
	}

	deviceResps = make([]*deviceResp, 0, len(deviceRespMap))
	for _, deviceResp := range deviceRespMap {
		deviceResps = append(deviceResps, deviceResp)
	}

	sort.Slice(deviceResps, func(i, j int) bool {

		return deviceResps[i].CreatedAt > deviceResps[j].CreatedAt
	})

	hasMore := false

	if len(deviceResps) > limit {
		hasMore = true
		if pre == 1 {
			deviceResps = deviceResps[len(deviceRespMap)-limit:]
		} else {
			deviceResps = deviceResps[:limit]
		}
	}

	c.JSON(http.StatusOK, deviceRespTotal{
		Data:  deviceResps,
		More:  wkutil.BoolToInt(hasMore),
		Total: 0,
	})

}

func (s *Server) requestDeviceSearch(path string, nodeId uint64, queryMap map[string]string, headers map[string]string) (*deviceRespTotal, error) {
	node := s.cfgServer.Node(nodeId)
	if node == nil {
		s.Error("requestDeviceSearch failed, node not found", zap.Uint64("nodeId", nodeId))
		return nil, errors.New("node not found")
	}
	fullUrl := fmt.Sprintf("%s%s", node.ApiServerAddr, path)
	queryMap["node_id"] = fmt.Sprintf("%d", nodeId)
	resp, err := network.Get(fullUrl, queryMap, headers)
	if err != nil {
		return nil, err
	}
	err = handlerIMError(resp)
	if err != nil {
		return nil, err
	}

	var deviceRespTotal *deviceRespTotal
	err = wkutil.ReadJSONByByte([]byte(resp.Body), &deviceRespTotal)
	if err != nil {
		return nil, err
	}
	return deviceRespTotal, nil
}
