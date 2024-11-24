import { defineStore } from 'pinia';

/* UserState */
export interface UserState {
  token: string;
  userInfo: {
    username: string;
    permissions?: string;
    exp?: number;
    token?: string;
  };
  permissionsList: PermissionItem[];
  systemSetting: {
    traceOn: boolean; // 日志是否开启trace
    lokiOn: boolean; // 日志是否开启loki
    prometheusOn: boolean; // 是否开启Prometheus
  };
}

interface PermissionItem {
  resource: string;
  action: string;
}

export const useUserStore = defineStore({
  id: 'user',
  state: (): UserState => ({
    token: '',
    userInfo: {
      username: '您好，超管'
    },
    permissionsList: [],
    systemSetting: {
      traceOn: false,
      lokiOn: false,
      prometheusOn: false
    }
  }),
  getters: {},
  actions: {
    // Set Token
    setToken(token: string) {
      this.token = token;
    },
    // Set setUserInfo
    setUserInfo(userInfo: UserState['userInfo']) {
      this.userInfo = userInfo;
    },
    setPermissions(str: string) {
      if (!str) {
        return;
      }
      const getPermissions: PermissionItem[] = [];
      str.split(',').forEach(item => {
        let arr = item.split(':');
        if (arr.length >= 2) {
          getPermissions.push({
            resource: arr[0],
            action: arr[1]
          });
        }
      });
      this.permissionsList = getPermissions;
    },
    hasPermission(resource: string, action: string) {
      for (let i = 0; i < this.permissionsList.length; i++) {
        let p = this.permissionsList[i];
        if ((p.resource === resource || p.resource == '*') && (p.action === action || p.action === '*')) {
          return true;
        }
      }
      return false;
    },
    setSystemSetting(setting: any) {
      this.systemSetting = {
        traceOn: setting.logger.trace_on === 1,
        lokiOn: setting.logger.loki_on === 1,
        prometheusOn: setting.prometheus_on === 1
      };
    },
    logout() {
      this.token = '';
      this.userInfo = {
        username: ''
      };
      this.permissionsList = [];
      this.systemSetting = {
        traceOn: false,
        lokiOn: false,
        prometheusOn: false
      };
    }
  },
  persist: true
});
