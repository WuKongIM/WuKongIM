import http from '@/api';

export const dataApi = {
  /**
   * 获取连接列表
   * @param param
   */
  connections: (param: any) => {
    return http.get<any>(`/connz`, param);
  },
  /**
   * 搜索用户
   * @param param
   */
  users: (param: any) => {
    return http.get<any>(`/cluster/users`, param);
  },
  /**
   * 获取用户的设备列表
   * @param param
   */
  devices: (param: any) => {
    return http.get<any>(`/cluster/devices`, param);
  },
  /**
   * 搜索消息
   * @param param
   */
  searchMessages: (param: any) => {
    return http.get<any>(`/cluster/messages`, param);
  },
  /**
   * 搜索频道
   * @param param
   */
  searchChannels: (param: any) => {
    return http.get<any>(`/cluster/channels`, param);
  },
  /**
   * 获取频道订阅者
   * @param channelId
   * @param channelType
   */
  subscribers(channelId: string, channelType: number) {
    return http.get<any>(`/cluster/channels/${channelId}/${channelType}/subscribers`);
  },
  /**
   * 获取频道白名单列表
   * @param channelId
   * @param channelType
   */
  allowlist(channelId: string, channelType: number) {
    return http.get<any>(`/cluster/channels/${channelId}/${channelType}/allowlist`);
  },
  /**
   * 获取频道黑名单列表
   * @param channelId
   * @param channelType
   */
  denylist(channelId: string, channelType: number) {
    return http.get<any>(`/cluster/channels/${channelId}/${channelType}/denylist`);
  },

  /**
   * 获取用户的会话列表
   * @param param
   */
  conversations: (param: any) => {
    return http.get<any>(`/cluster/conversations`, param);
  }
};
