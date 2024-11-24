import http from '@/api';

import type { ResultData } from '@/api/interface';

export const loginApi = {
  /**
   * 登录
   * @param param
   */
  login: (param: any) => {
    return http.post<ResultData>(`/manager/login`, param);
  }
};
