package cluster

import "github.com/WuKongIM/WuKongIM/pkg/wkstore"

func (s *Server) LoadLastMsgs(channelID string, channelType uint8, limit int) ([]wkstore.Message, error) {
	return s.channelManager.messageStorage.db.LoadLastMsgs(channelID, channelType, limit)
}
