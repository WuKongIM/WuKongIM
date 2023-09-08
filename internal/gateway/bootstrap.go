package gateway

// type bootstrapStatus int

// const (
// 	bootstrapStatusInit bootstrapStatus = iota
// 	bootstrapStatusWaitConnect
// 	bootstrapStatusGetClusterconfigSet
// 	bootstrapStatusSaveClusterconfig
// 	bootstrapStatusReady
// )

// type bootstrap struct {
// 	status            bootstrapStatus
// 	bootstrapNodeAddr string
// 	clusterStorePath  string
// 	nodeID            NodeID
// 	wklog.Log
// }

// func newBootstrap(nodeID NodeID, bootstrapNodeAddr string, clusterStorePath string) *bootstrap {
// 	return &bootstrap{
// 		nodeID:            nodeID,
// 		bootstrapNodeAddr: bootstrapNodeAddr,
// 		clusterStorePath:  clusterStorePath,
// 		Log:               wklog.NewWKLog("bootstrap"),
// 	}
// }

// func (b *bootstrap) bootstrap() error {

// 	var (
// 		bootstrapcli     *client.Client
// 		errorSleep       = time.Millisecond * 500
// 		err              error
// 		clusterconfigSet *pb.ClusterConfigSet
// 	)
// 	for {
// 		switch b.status {
// 		case bootstrapStatusInit:
// 			_, err := os.Stat(b.clusterStorePath)
// 			if err != nil {
// 				if os.IsNotExist(err) {
// 					b.status = bootstrapStatusWaitConnect
// 				} else {
// 					b.Error("stat cluster store path is error", zap.Error(err))
// 					time.Sleep(errorSleep)
// 					continue
// 				}
// 			} else {
// 				b.status = bootstrapStatusReady
// 			}
// 		case bootstrapStatusWaitConnect:
// 			bootstrapcli, err = b.connectBootstrapdNode()
// 			if err != nil {
// 				b.Error("connect bootstrap node is error", zap.Error(err))
// 				time.Sleep(errorSleep)
// 				continue
// 			}
// 			b.status = bootstrapStatusGetClusterconfigSet
// 		case bootstrapStatusGetClusterconfigSet:
// 			clusterconfigSet, err = b.getClusterConfigSet(bootstrapcli)
// 			if err != nil {
// 				b.Error("get cluster config set is error", zap.Error(err))
// 				time.Sleep(errorSleep)
// 				continue
// 			}
// 			b.status = bootstrapStatusSaveClusterconfig
// 		case bootstrapStatusSaveClusterconfig:
// 			err = b.saveClusterConfigSet(clusterconfigSet)
// 			if err != nil {
// 				b.Error("save cluster config set is error", zap.Error(err))
// 				time.Sleep(errorSleep)
// 				continue
// 			}
// 			b.status = bootstrapStatusReady
// 		case bootstrapStatusReady:
// 			return nil
// 		}

// 	}
// }

// func (b *bootstrap) connectBootstrapdNode() (*client.Client, error) {
// 	if strings.TrimSpace(b.bootstrapNodeAddr) == "" {
// 		return nil, errors.New("bootstrap node addr is empty")
// 	}
// 	bootstrapcli := client.New(b.bootstrapNodeAddr, client.WithUID(b.nodeID.String()))
// 	err := bootstrapcli.Connect()
// 	if err != nil {
// 		return nil, err
// 	}
// 	return bootstrapcli, nil
// }

// func (b *bootstrap) getClusterConfigSet(cli *client.Client) (*pb.ClusterConfigSet, error) {
// 	resp, err := cli.Request("/getclusterconfigset", nil)
// 	if err != nil {
// 		return nil, err
// 	}
// 	if resp.Status != proto.Status_OK {
// 		return nil, errors.New("get cluster config set is error")
// 	}
// 	clusterconfigSet := &pb.ClusterConfigSet{}
// 	err = clusterconfigSet.Unmarshal(resp.Body)
// 	if err != nil {
// 		return nil, err
// 	}
// 	return clusterconfigSet, nil
// }

// func (b *bootstrap) saveClusterConfigSet(clusterconfigSet *pb.ClusterConfigSet) error {
// 	clusterStorePathTmp := b.clusterStorePath + ".tmp"
// 	f, err := os.Create(clusterStorePathTmp)
// 	if err != nil {
// 		return err
// 	}
// 	_, err = f.WriteString(wkutil.ToJSON(clusterconfigSet))
// 	if err != nil {
// 		return err
// 	}
// 	return os.Rename(clusterStorePathTmp, b.clusterStorePath)
// }
