package store

import (
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/wkdb"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/lni/goutils/syncutil"
	"go.uber.org/zap"
)

type Store struct {
	opts *Options
	wklog.Log

	wdb wkdb.DB

	channelCfgCh    chan *channelCfgReq
	stopper         *syncutil.Stopper
	recoveryManager *RecoveryManager
}

func New(opts *Options) *Store {
	s := &Store{
		opts:         opts,
		Log:          wklog.NewWKLog("store"),
		wdb:          opts.DB,
		channelCfgCh: make(chan *channelCfgReq, 2048),
		stopper:      syncutil.NewStopper(),
	}

	// 初始化恢复管理器
	s.recoveryManager = NewRecoveryManager(s)

	return s
}

func (s *Store) NextPrimaryKey() uint64 {
	return s.wdb.NextPrimaryKey()
}

func (s *Store) DB() wkdb.DB {
	return s.wdb
}

func (s *Store) Start() error {
	for i := 0; i < 50; i++ {
		go s.loopSaveChannelClusterConfig()
	}
	// 启动定期清理旧删除记录的任务
	s.stopper.RunWorker(s.loopCleanupOldDeleteLogs)
	// s.stopper.RunWorker(s.loopSaveChannelClusterConfig)
	return nil
}

func (s *Store) Stop() {
	s.stopper.Stop()
}

// RecoverChannelFromDeleteLogs 从删除日志恢复频道（导出方法）
func (s *Store) RecoverChannelFromDeleteLogs(channelId string, channelType uint8, lastAppliedLogIndex uint64) error {
	return s.recoveryManager.RecoverChannelFromDeleteLogs(channelId, channelType, lastAppliedLogIndex)
}

// loopCleanupOldDeleteLogs 定期清理旧的删除记录
func (s *Store) loopCleanupOldDeleteLogs() {
	// 每天执行一次清理
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	// 启动时立即执行一次清理
	s.cleanupOldDeleteLogsOnce()

	for {
		select {
		case <-ticker.C:
			s.cleanupOldDeleteLogsOnce()
		case <-s.stopper.ShouldStop():
			return
		}
	}
}

// cleanupOldDeleteLogsOnce 执行一次删除记录清理
func (s *Store) cleanupOldDeleteLogsOnce() {
	// 清理 30 天前的删除记录
	// 这个时间可以根据实际情况调整，或者配置化
	beforeTime := time.Now().AddDate(0, 0, -30).Unix()

	s.Info("开始清理旧删除记录", zap.Time("beforeDate", time.Unix(beforeTime, 0)))

	err := s.wdb.CleanupOldDeleteLogs(beforeTime)
	if err != nil {
		s.Error("清理旧删除记录失败", zap.Error(err))
		return
	}

	// 获取当前删除记录总数（用于监控）
	count, err := s.wdb.GetDeleteLogsCount()
	if err != nil {
		s.Warn("获取删除记录总数失败", zap.Error(err))
	} else {
		s.Info("删除记录清理完成", zap.Int64("currentCount", count))
	}
}

type channelCfgReq struct {
	cfg   wkdb.ChannelClusterConfig
	errCh chan error
}
