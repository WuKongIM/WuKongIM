import http from '@/api';

export const monitorApi = {
  /**
   * 获取app监控数据
   */
  apppMetrics: (param: any) => {
    return http.get<any>('/metrics/app', param);
  },
  /**
   * 获取分布式监控数据
   */
  clusterMetrics: (param: any) => {
    return http.get<any>('/metrics/cluster', param);
  },
  /**
   * 获取系统监控数据
   */
  systemMetrics: (param: any) => {
    return http.get<any>('/metrics/system', param);
  },
  /**
   * 获取消息轨迹日志
   */
  messageTraces: (param: any) => {
    return http.get<any>('/cluster/message/trace', param);
  },
  /**
   * 消息回执轨迹日志
   */
  messageRecvackTraces: (param: any) => {
    return http.get<any>('/cluster/message/trace/recvack', param);
  }
};
