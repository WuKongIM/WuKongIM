package server

import (
	"sync"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wkstore"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	"github.com/pkg/errors"
	"go.uber.org/zap"
)

// PeerInFlightData PeerInFlightData
type PeerInFlightData struct {
	wkstore.PeerInFlightDataModel
	pri   int64 // 优先级的时间点 值越小越优先
	index int
}

// PeerInFlightQueue 正在投递的节点消息的队列
type PeerInFlightQueue struct {
	s             *Server
	infFlights    sync.Map
	inFlightPQ    peerInFlightDataPqueue
	inFlightData  map[string]*PeerInFlightData
	inFlightMutex sync.Mutex
	wklog.Log
}

// NewPeerInFlightQueue NewPeerInFlightQueue
func NewPeerInFlightQueue(s *Server) *PeerInFlightQueue {
	return &PeerInFlightQueue{
		s:            s,
		infFlights:   sync.Map{},
		Log:          wklog.NewWKLog("PeerInFlightQueue"),
		inFlightData: map[string]*PeerInFlightData{},
		inFlightPQ:   newpeerInFlightDataPqueue(1024),
	}
}

// startInFlightTimeout startInFlightTimeout
func (n *PeerInFlightQueue) startInFlightTimeout(data *PeerInFlightData) {
	now := time.Now()
	data.pri = now.Add(n.s.opts.Cluster.PeerRPCMsgTimeout).UnixNano()
	n.pushInFlightMessage(data)
	n.addToInFlightPQ(data)

}

func (n *PeerInFlightQueue) addToInFlightPQ(data *PeerInFlightData) {
	n.inFlightMutex.Lock()
	defer n.inFlightMutex.Unlock()
	n.inFlightPQ.Push(data)

}
func (n *PeerInFlightQueue) pushInFlightMessage(data *PeerInFlightData) {
	n.inFlightMutex.Lock()
	defer n.inFlightMutex.Unlock()
	_, ok := n.inFlightData[data.No]
	if ok {
		return
	}
	n.inFlightData[data.No] = data

}

func (n *PeerInFlightQueue) finishMessage(no string) error {
	data, err := n.popInFlightMessage(no)
	if err != nil {
		return err
	}
	n.removeFromInFlightPQ(data)
	return nil
}
func (n *PeerInFlightQueue) removeFromInFlightPQ(data *PeerInFlightData) {
	n.inFlightMutex.Lock()
	if data.index == -1 {
		// this item has already been popped off the pqueue
		n.inFlightMutex.Unlock()
		return
	}
	n.inFlightPQ.Remove(data.index)
	n.inFlightMutex.Unlock()
}
func (n *PeerInFlightQueue) processInFlightQueue(t int64) {
	for {
		n.inFlightMutex.Lock()
		data, _ := n.inFlightPQ.PeekAndShift(t)
		n.inFlightMutex.Unlock()
		if data == nil {
			break
		}
		err := n.finishMessage(data.No)
		if err != nil {
			break
		}
		// 开始投递
		n.s.startDeliveryPeerData(data)
	}
}

func (n *PeerInFlightQueue) popInFlightMessage(no string) (*PeerInFlightData, error) {
	n.inFlightMutex.Lock()
	defer n.inFlightMutex.Unlock()
	msg, ok := n.inFlightData[no]
	if !ok {
		return nil, errors.New("ID not in flight")
	}
	delete(n.inFlightData, no)
	return msg, nil
}

// Start 开始运行重试
func (n *PeerInFlightQueue) Start() {
	peerInFlightDatas, err := n.s.store.GetPeerInFlightData() // TODO: 分布式情况多节点下，这里存在重复投递的可能，但是就算重复投递，客户端有去重机制所以也不影响，后面可以修正
	if err != nil {
		panic(err)
	}
	err = n.s.store.ClearPeerInFlightData()
	if err != nil {
		panic(err)
	}

	if len(peerInFlightDatas) > 0 {
		for _, peerInFlightDataModel := range peerInFlightDatas {
			n.startInFlightTimeout(&PeerInFlightData{
				PeerInFlightDataModel: *peerInFlightDataModel,
			})
		}

	}
	n.s.Schedule(n.s.opts.Cluster.PeerRPCTimeoutScanInterval, func() {
		now := time.Now().UnixNano()
		n.processInFlightQueue(now)
	})
}

// Stop Stop
func (n *PeerInFlightQueue) Stop() {
	n.Debug("stop...")
	datas := make([]*wkstore.PeerInFlightDataModel, 0)
	n.infFlights.Range(func(key, value interface{}) bool {
		datas = append(datas, &value.(*PeerInFlightData).PeerInFlightDataModel)
		return true
	})
	if len(datas) > 0 {
		n.Warn("存在节点投递数据未投递。", zap.Int("count", len(datas)))
		err := n.s.store.AddPeerInFlightData(datas)
		if err != nil {
			n.Error("异常退出", zap.Error(err), zap.String("data", wkutil.ToJSON(datas)))
			return
		}
	}
	n.Info("正常退出")
}

type peerInFlightDataPqueue []*PeerInFlightData

func newpeerInFlightDataPqueue(capacity int) peerInFlightDataPqueue {
	return make(peerInFlightDataPqueue, 0, capacity)
}

func (pq peerInFlightDataPqueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *peerInFlightDataPqueue) Push(x *PeerInFlightData) {
	n := len(*pq)
	c := cap(*pq)
	if n+1 > c {
		npq := make(peerInFlightDataPqueue, n, c*2)
		copy(npq, *pq)
		*pq = npq
	}
	*pq = (*pq)[0 : n+1]
	x.index = n
	(*pq)[n] = x
	pq.up(n)
}

func (pq *peerInFlightDataPqueue) Pop() *PeerInFlightData {
	n := len(*pq)
	c := cap(*pq)
	pq.Swap(0, n-1)
	pq.down(0, n-1)
	if n < (c/2) && c > 25 {
		npq := make(peerInFlightDataPqueue, n, c/2)
		copy(npq, *pq)
		*pq = npq
	}
	x := (*pq)[n-1]
	x.index = -1
	*pq = (*pq)[0 : n-1]
	return x
}

func (pq *peerInFlightDataPqueue) Remove(i int) *PeerInFlightData {
	n := len(*pq)
	if n-1 != i {
		pq.Swap(i, n-1)
		pq.down(i, n-1)
		pq.up(i)
	}
	x := (*pq)[n-1]
	x.index = -1
	*pq = (*pq)[0 : n-1]
	return x
}

func (pq *peerInFlightDataPqueue) PeekAndShift(max int64) (*PeerInFlightData, int64) {
	if len(*pq) == 0 {
		return nil, 0
	}

	x := (*pq)[0]
	if x.pri > max {
		return nil, x.pri - max
	}
	pq.Pop()

	return x, 0
}

func (pq *peerInFlightDataPqueue) up(j int) {
	for {
		i := (j - 1) / 2 // parent
		if i == j || (*pq)[j].pri >= (*pq)[i].pri {
			break
		}
		pq.Swap(i, j)
		j = i
	}
}

func (pq *peerInFlightDataPqueue) down(i, n int) {
	for {
		j1 := 2*i + 1
		if j1 >= n || j1 < 0 { // j1 < 0 after int overflow
			break
		}
		j := j1 // left child
		if j2 := j1 + 1; j2 < n && (*pq)[j1].pri >= (*pq)[j2].pri {
			j = j2 // = 2*i + 2  // right child
		}
		if (*pq)[j].pri >= (*pq)[i].pri {
			break
		}
		pq.Swap(i, j)
		i = j
	}
}
