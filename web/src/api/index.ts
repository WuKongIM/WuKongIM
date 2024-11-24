import { useUserStore } from '@/stores/modules/user';
import { LOGIN_URL, WK_CONFIG } from '@/config';
import router from '@/router';
import { ResultEnum } from '@/enums/http-enum';

import axios from 'axios';

import { ElMessage } from 'element-plus';

import type { AxiosError, AxiosInstance, AxiosRequestConfig, AxiosResponse, InternalAxiosRequestConfig } from 'axios';

export interface CustomAxiosRequestConfig extends InternalAxiosRequestConfig {
  isSaas?: boolean;
}

const userStore = useUserStore();

const axiosConfig = {
  baseURL: WK_CONFIG.API_URL, // 默认地址请求地址
  timeout: ResultEnum.TIMEOUT as number, // 设置超时时间
  withCredentials: false // 跨域请求时是否需要使用凭证
};

class RequestHttp {
  service: AxiosInstance;
  public constructor(config: AxiosRequestConfig) {
    this.service = axios.create(config);

    /**
     * @description 请求拦截器
     * 客户端发送请求 -> [请求拦截器] -> 服务器
     * token校验(JWT) : 接受服务器返回的 token,存储到 pinia/本地储存当中
     */
    this.service.interceptors.request.use(
      (config: CustomAxiosRequestConfig) => {
        // 添加token
        if (userStore.token) {
          config.headers['Authorization'] = 'Bearer ' + userStore.token;
        }
        return config;
      },
      (error: AxiosError) => {
        return Promise.reject(error);
      }
    );

    /**
     * @description 响应拦截器
     *  服务器换返回信息 -> [拦截统一处理] -> 客户端JS获取到信息
     */
    this.service.interceptors.response.use(
      (response: AxiosResponse & { config: CustomAxiosRequestConfig }) => {
        const { data } = response;
        return Promise.resolve(data);
      },
      (error: any) => {
        const code = error.response.status;
        if (code == 401) {
          userStore.setToken('');
          userStore.setUserInfo({ username: '您好，超管' });
          ElMessage({
            message: '登录失效，请重新登录！',
            grouping: true,
            plain: true,
            type: 'error'
          });
          return router.replace(LOGIN_URL);
        }

        if (code == 400) {
          ElMessage({
            message: error.response.data.msg,
            grouping: true,
            plain: true,
            type: 'error'
          });
          return Promise.reject(error.response.data);
        }
        // 响应失败
        return Promise.reject(error);
      }
    );
  }

  get<T>(url: string, params?: object, _object = {}): Promise<T> {
    return this.service.get(url, { params, ..._object });
  }

  post<T>(url: string, params?: object | string, _object = {}): Promise<T> {
    return this.service.post(url, params, _object);
  }

  put<T>(url: string, params?: object, _object = {}): Promise<T> {
    return this.service.put(url, params, _object);
  }

  delete<T>(url: string, params?: any, _object = {}): Promise<T> {
    return this.service.delete(url, { params, ..._object });
  }

  download(url: string, params?: object, _object = {}): Promise<BlobPart> {
    return this.service.post(url, params, { ..._object, responseType: 'blob' });
  }
}

export default new RequestHttp(axiosConfig);
