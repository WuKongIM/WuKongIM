import http from '@/api';

export const clusterApi = {
  /**
   * 获取集群节点信息
   */
  nodes: () => {
    return http.get<any>(`/cluster/nodes`);
  },
  /**
   * 获取所有槽
   */
  slots: () => {
    return http.get<any>(`/cluster/allslot`);
  },
  /**
   * 迁移槽
   * @param param
   */
  migrateSlot: (param: { slot: number; migrateFrom: number; migrateTo: number }) => {
    return http.post<any>(`/cluster/slots/${param.slot}/migrate`, {
      migrate_from: param.migrateFrom,
      migrate_to: param.migrateTo
    });
  },
  /**
   * 获取简单节点信息
   */
  simpleNodes: () => {
    return http.get<any>(`/cluster/simpleNodes`);
  },
  /**
   * 获取节点的频道配置列表
   */
  nodeChannelConfigs: (param: any) => {
    return http.get<any>(`/cluster/nodes/${param.nodeId}/channels`, param);
  },
  /**
   * 迁移频道
   * @param param
   */
  migrateChannel: (param: { channelId: string; channelType: number; migrateFrom: number; migrateTo: number }) => {
    return http.post<any>(`/cluster/channels/${param.channelId}/${param.channelType}/migrate`, {
      migrate_from: param.migrateFrom,
      migrate_to: param.migrateTo
    });
  },
  /**
   * 获取频道分布式配置
   * @param param
   */
  channelClusterConfig: (param: { channelId: string; channelType: number; nodeId?: number }) => {
    return http.get(`/cluster/channels/${param.channelId}/${param.channelType}/config?node_id=${param.nodeId || 0}`);
  },
  /**
   * 开启频道
   * @param param
   */
  channelStart: (param: { channelId: string; channelType: number; nodeId?: number }) => {
    return http.post<any>(`/cluster/channels/${param.channelId}/${param.channelType}/start`, { node_id: param.nodeId });
  },
  /**
   * 停止频道
   * @param param
   */
  channelStop: (param: { channelId: string; channelType: number; nodeId?: number }) => {
    return http.post<any>(`/cluster/channels/${param.channelId}/${param.channelType}/stop`, { node_id: param.nodeId });
  },
  /**
   * 频道副本列表
   * @param param
   */
  channelReplicas: (param: { channelId: string; channelType: number }) => {
    return http.get<any>(`/cluster/channels/${param.channelId}/${param.channelType}/replicas`);
  },
  /**
   * 获取集群配置
   */
  clusterConfig: () => {
    return http.get<any>(`/cluster/info`);
  },
  /**
   * 获取日志
   * @param param
   */
  clusterLogs: (param: any) => {
    return http.get<any>(`/cluster/logs`, param);
  }
};
