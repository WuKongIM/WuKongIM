import http from '@/api';

import type { ResultData } from '@/api/interface';

export const systemApi = {
  /**
   * 获取系统设置
   * @param param
   */
  systemSettings: () => {
    return http.get<ResultData>(`/varz/setting`);
  }
};
