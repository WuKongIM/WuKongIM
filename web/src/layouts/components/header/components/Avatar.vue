<script lang="ts" setup>
import { useRouter } from 'vue-router';
import { useUserStore } from '@/stores/modules/user';
import { LOGIN_URL } from '@/config';

const router = useRouter();
const userStore = useUserStore();
const username = ref('您好，超管');

// 退出登录
const onLogoutClick = () => {
  // 1.清除 Token
  userStore.setToken('');
  // 2.重定向到登陆页
  router.replace(LOGIN_URL);
};
</script>

<template>
  <el-dropdown trigger="click" placement="bottom-end">
    <div class="flex items-center cursor-pointer">
      <div class="avatar relative">
        <img src="https://unpkg.com/@vbenjs/static-source@0.1.7/source/avatar-v1.webp" alt="avatar" />
        <span class="wk-badge !size-8px"></span>
      </div>
    </div>

    <template #dropdown>
      <el-dropdown-menu class="w-238px">
        <div class="pl-15px pr-15px pt-10px pb-10px flex">
          <div class="inline-flex items-center justify-center w-48px h-48px bg-secondary overflow-hidden rounded-full relative">
            <img
              class="h-full w-full object-cover"
              src="https://unpkg.com/@vbenjs/static-source@0.1.7/source/avatar-v1.webp"
              alt="logo"
            />
            <span class="wk-badge"></span>
          </div>
          <div class="flex-1 ml-10px">
            <div class="mt-4px flex items-center text-sm font-medium">
              {{ username }}
            </div>
            <div class="mt-8px text-xs font-normal wk-role">角色：管理员</div>
          </div>
        </div>
        <el-dropdown-item divided>
          <el-icon class="mt-2px" :size="16">
            <i-wk-book-open theme="outline" />
          </el-icon>
          文档
        </el-dropdown-item>
        <el-dropdown-item>
          <el-icon class="mt-2px" :size="16">
            <i-wk-help theme="outline" size="24" />
          </el-icon>
          问题 & 帮助
        </el-dropdown-item>
        <el-dropdown-item divided @click="onLogoutClick">
          <el-icon class="mt-3px" :size="16">
            <i-wk-power theme="outline" />
          </el-icon>
          退出登录
        </el-dropdown-item>
      </el-dropdown-menu>
    </template>
  </el-dropdown>
</template>

<style lang="scss" scoped>
.username {
  margin: 0 16px;
  font-size: 14px;
  height: 32px;
  line-height: 32px;
}
.avatar {
  width: 32px;
  height: 32px;
  overflow: hidden;
  border-radius: 50%;
  img {
    width: 100%;
    height: 100%;
  }
}

.wk-role {
  color: var(--el-text-color-regular);
}

.wk-badge {
  position: absolute;
  background-color: rgb(87, 209, 136);
  vertical-align: middle;
  border-radius: 50%;
  width: 10px;
  height: 10px;
  display: inline-block;
  bottom: 6px;
  right: 6px;
}
</style>
