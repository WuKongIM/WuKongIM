<script lang="ts" name="Main" setup>
import { useRoute } from 'vue-router';
import { useAuthStore } from '@/stores/modules/auth';
import { useGlobalStore } from '@/stores/modules/global';

import { WK_CONFIG } from '@/config';
// 组件
import ToolBarLeft from '@/layouts/components/Header/ToolBarLeft.vue';
import ToolBarRight from '@/layouts/components/Header/ToolBarRight.vue';
import Main from '@/layouts/components/Main.vue';
import SubMenu from '@/layouts/components/menu/SubMenu.vue';

const route = useRoute();
const authStore = useAuthStore();
const globalStore = useGlobalStore();
const isCollapse = computed(() => globalStore.isCollapse);
const menuList = computed(() => authStore.showMenuListGet);
const activeMenu = computed(() => (route.meta.activeMenu ? route.meta.activeMenu : route.path) as string);
const APP_TITLE = WK_CONFIG.APP_TITLE;
</script>

<template>
  <el-container class="layout">
    <el-aside>
      <div class="aside-box w-224px">
        <div class="logo flex-center">
          <img class="logo-img" src="/logo.png" alt="logo" />
          <span class="logo-text">{{ APP_TITLE }}</span>
        </div>

        <el-scrollbar>
          <el-menu
            :default-active="activeMenu"
            :collapse="isCollapse"
            :router="false"
            :unique-opened="true"
            :collapse-transition="false"
          >
            <SubMenu :menu-list="menuList" />
          </el-menu>
        </el-scrollbar>
      </div>
    </el-aside>
    <el-container>
      <el-header>
        <ToolBarLeft />
        <ToolBarRight />
      </el-header>
      <Main />
    </el-container>
  </el-container>
</template>

<style lang="scss" scoped>
.el-container {
  width: 100%;
  height: 100%;

  :deep(.el-aside) {
    width: auto;
    background-color: var(--el-menu-bg-color);
    border-right: 1px solid var(--el-aside-border-color);
    .aside-box {
      display: flex;
      flex-direction: column;
      height: 100%;
      transition: width 0.3s ease;
      .el-scrollbar {
        height: calc(100% - 55px);
        .el-menu {
          width: 100%;
          overflow-x: hidden;
          border-right: none;
        }
      }
      .logo {
        box-sizing: border-box;
        height: 55px;
        .logo-img {
          width: 28px;
          object-fit: contain;
          margin-right: 6px;
        }
        .logo-text {
          font-size: 20px;
          font-weight: bold;
          color: var(--el-aside-logo-text-color);
          white-space: nowrap;
        }
      }
    }
  }
  .el-header {
    box-sizing: border-box;
    display: flex;
    align-items: center;
    justify-content: space-between;
    height: 50px;
    padding: 0 12px;
    background-color: var(--el-header-bg-color);
    border-bottom: 1px solid var(--el-header-border-color);
    :deep(.tool-bar-ri) {
      .toolBar-icon,
      .username {
        color: var(--el-header-text-color);
      }
    }
  }
}
</style>
