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
}

export const useUserStore = defineStore({
  id: 'user',
  state: (): UserState => ({
    token: '',
    userInfo: {
      username: '您好，超管'
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
    }
  },
  persist: true
});
