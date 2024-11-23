import { useUserStore } from '@/stores/modules/user';
import { useAuthStore } from '@/stores/modules/auth';

import { LOGIN_URL, ROUTER_WHITE_LIST } from '@/config';

import { createRouter, createWebHistory } from 'vue-router';

import NProgress from '@/utils/nprogress';

import routes from './routers';

/**
 * @description 📚 路由参数配置简介
 * @param path ==> 菜单路径
 * @param name ==> 菜单别名
 * @param redirect ==> 重定向地址
 * @param component ==> 视图文件路径
 * @param meta ==> 菜单信息
 * @param meta.icon ==> 菜单图标
 * @param meta.title ==> 菜单标题
 * @param meta.activeMenu ==> 当前路由为详情页时，需要高亮的菜单
 * @param meta.isLink ==> 是否外链
 * @param meta.isHide ==> 是否隐藏
 * @param meta.isFull ==> 是否全屏(示例：数据大屏页面)
 * @param meta.isAffix ==> 是否固定在 tabs nav
 * @param meta.isKeepAlive ==> 是否缓存
 * */
const router = createRouter({
  history: createWebHistory('/web'),
  routes,
  strict: false,
  scrollBehavior: () => ({ left: 0, top: 0 })
});

/**
 * @description 路由拦截 beforeEach
 * */
router.beforeEach(async (to, from, next) => {
  const authStore = useAuthStore();
  const userStore = useUserStore();

  // NProgress 开始
  NProgress.start();

  /** 如果已经登录并存在登录信息后不能跳转到路由白名单，而是继续保持在当前页面 */
  function toCorrectRoute() {
    ROUTER_WHITE_LIST.includes(to.fullPath) ? next(from.fullPath) : next();
  }

  if (userStore.token) {
    // 正常访问页面
    if (!authStore.authMenuListGet.length) {
      await authStore.getAuthMenuList();
    }
    toCorrectRoute();
  } else {
    if (to.path !== LOGIN_URL) {
      if (ROUTER_WHITE_LIST.indexOf(to.path) !== -1) {
        next();
      } else {
        next({ path: LOGIN_URL, replace: true });
      }
    } else {
      next();
    }
  }
});

/**
 * @description 路由跳转错误
 * */
router.onError(error => {
  NProgress.done();
  console.warn('路由错误', error.message);
});

/**
 * @description 路由跳转结束
 * */
router.afterEach(() => {
  NProgress.done();
});

export default router;
