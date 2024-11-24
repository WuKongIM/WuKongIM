<script lang="ts" setup>
import { useRouter } from 'vue-router';
import type { FormInstance, FormRules } from 'element-plus';

import { useUserStore } from '@/stores/modules/user';

import { WK_CONFIG, HOME_URL } from '@/config';

// API接口
import { loginApi } from '@/api/modules/login-api';

const APP_TITLE = WK_CONFIG.APP_TITLE_LOGIN;

const router = useRouter();
const userStore = useUserStore();

interface RuleForm {
  username: string;
  password: string;
}

// 登录表单
const formRef = ref<FormInstance>();
const loginForm = reactive<RuleForm>({
  username: '',
  password: ''
});
const rules = reactive<FormRules<RuleForm>>({
  username: [{ required: true, message: '请输入账号', trigger: 'blur' }],
  password: [{ required: true, message: '请输入密码', trigger: 'blur' }]
});
const loginButLoading = ref(false);

// 登录
const onLoginClick = async (formEl: FormInstance | undefined) => {
  if (!formEl) return;
  loginButLoading.value = true;
  await formEl.validate(async valid => {
    loginButLoading.value = false;
    if (valid) {
      const res = await loginApi.login(loginForm) as any;
      userStore.setUserInfo(res);
      userStore.setToken(res.token);
      userStore.setPermissions(res.permissions);
      await router.push(HOME_URL);
    }
  });
};
</script>

<template>
  <div class="wh-full lex flex-1 flex-col justify-center items-center overflow-hidden">
    <div class="flex-center wh-full relative">
      <div class="login-bg"></div>
      <div class="flex flex-col items-center justify-center relative">
        <el-card class="w-460px !border-none" body-style="padding: 48px;">
          <div class="flex justify-center mb-12px">
            <img class="h-64px w-64px" src="/logo.png" alt="logo" />
          </div>

          <div class="flex justify-center mb-18px">
            <h3 class="text-24px app-text-fg-high mt-0 mb-24px" style="align-self: flex-start; font-weight: 500">
              {{ APP_TITLE }}
            </h3>
          </div>

          <el-form ref="formRef" :model="loginForm" :rules="rules" size="large" autocomplete="on">
            <el-form-item prop="username">
              <el-input v-model="loginForm.username" placeholder="请输入账号">
                <template #prefix>
                  <el-icon class="el-input__icon">
                    <i-wk-people theme="outline" size="24" />
                  </el-icon>
                </template>
              </el-input>
            </el-form-item>
            <el-form-item prop="password">
              <el-input
                v-model="loginForm.password"
                type="password"
                placeholder="请输入密码"
                show-password
                @keyup.enter="onLoginClick(formRef)"
              >
                <template #prefix>
                  <el-icon class="el-input__icon">
                    <i-wk-lock theme="outline" size="24" />
                  </el-icon>
                </template>
              </el-input>
            </el-form-item>
            <el-form-item>
              <div class="flex justify-between">
                <div class="flex-initial">演示账号：guest 密码：guest</div>
              </div>
            </el-form-item>
            <el-form-item>
              <el-button type="primary" :loading="loginButLoading" class="w-full" @click="onLoginClick(formRef)">
                登录
              </el-button>
            </el-form-item>
          </el-form>
        </el-card>
      </div>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.login-bg {
  width: 100%;
  height: 100%;
  position: absolute;
  background: linear-gradient(154deg, #07070915 30%, hsl(var(--primary) / 30%) 48%, #07070915 64%);
  filter: blur(100px);
}
</style>

<route lang="yaml">
meta:
  title: 登录
  layout: false
</route>
