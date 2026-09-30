<script setup lang="ts">
import { ref } from 'vue'
import { t } from '../i18n'
import { demoLogoURL } from '../services/assets'
import APIClient from '../services/APIClient'
import { WKSDK } from 'wukongimjssdk';
import { establishSession, loadSession } from '../services/session'
import { demoHomeURL } from '../../../shared/home'
import '../../../shared/home.css'


const saved = loadSession(window.sessionStorage)
const home = demoHomeURL(import.meta.env.DEV)
const requestedURL = new URLSearchParams(window.location.search).get('apiurl')?.trim()
const apiAddr = ref(requestedURL || saved?.apiURL || (import.meta.env.DEV ? 'http://127.0.0.1:5001' : window.location.origin))
const username = ref(saved?.uid || '')
const password = ref(saved?.token || '')
const createDemoCredentials = ref(false)
const submitting = ref(false)
const errorMessage = ref('')

const login = async () => {
  if (submitting.value) return
  submitting.value = true
  errorMessage.value = ''
  try {
    const session = await establishSession(window.sessionStorage, {
      apiURL: apiAddr.value, uid: username.value, token: password.value,
    }, createDemoCredentials.value, async session => {
      APIClient.shared.config.apiURL = session.apiURL
      await APIClient.shared.post('/user/token', {
        uid: session.uid, token: session.token, device_flag: 1, device_level: 0,
      })
    })
    APIClient.shared.config.apiURL = session.apiURL
    // Recreate the SDK singleton so another account cannot inherit cached messages.
    window.location.replace(window.location.pathname + window.location.search + '#/chat')
  } catch {
    errorMessage.value = t('loginFailed')
  } finally {
    submitting.value = false
  }
}



</script>
<template>
  <main class="login-page">
    <section class="login-card">
      <a class="demo-home-link" :href="home" data-demo-home><span aria-hidden="true">←</span>{{ t('backToHome') }}</a>
      <div class="login-brand"><img :src="demoLogoURL" :alt="t('logo')" /><span>WuKongIM</span></div>
      <h1>{{ t('loginTitle') }}</h1><p class="intro">{{ t('loginIntro') }}</p>
      <form @submit.prevent="login">
        <label for="api-address">{{ t('apiAddress') }}</label>
        <input id="api-address" type="url" :placeholder="t('apiAddressPlaceholder')" v-model="apiAddr" required />
        <label for="account-uid">{{ t('username') }}</label>
        <input id="account-uid" type="text" autocomplete="username" :placeholder="t('usernamePlaceholder')" v-model="username" required />
        <label for="account-token">{{ t('password') }}</label>
        <input id="account-token" type="password" autocomplete="off" :placeholder="t('passwordPlaceholder')" v-model="password" required />
        <p class="token-hint">{{ t('existingTokenHint') }}</p>
        <label class="demo-credentials"><input type="checkbox" v-model="createDemoCredentials" />{{ t('createDemoCredentials') }}</label>
        <p v-if="errorMessage" class="login-error" role="alert">{{ errorMessage }}</p>
        <button class="primary" type="submit" :disabled="submitting">{{ t(submitting ? 'loggingIn' : 'login') }} <span aria-hidden="true">→</span></button>
      </form>
      <footer><span>SDK v{{ WKSDK.shared().config.sdkVersion }}</span><a href="https://github.com/WuKongIM/WuKongIM" target="_blank" rel="noopener">GitHub ↗</a></footer>
    </section>
  </main>
</template>

<style scoped>
.login-page { height: 100%; overflow-y: auto; display: grid; align-items: center; justify-items: center; padding: max(24px, env(safe-area-inset-top)) 20px max(24px, env(safe-area-inset-bottom)); background: radial-gradient(ellipse at 25% 0, var(--accent-soft), transparent 60%), var(--chat-background); }
.login-card { width: min(100%, 440px); padding: 34px; border: 1px solid var(--line); border-radius: 24px; background: var(--surface); box-shadow: var(--shadow); }
.login-brand { display: flex; align-items: center; gap: 10px; font-size: 18px; font-weight: 700; letter-spacing: -.5px; }
.login-card > .demo-home-link { margin-bottom: 24px; }
.login-brand img { width: 40px; height: 40px; border-radius: 12px; }
h1 { font-size: 26px; letter-spacing: -.8px; margin: 30px 0 8px; }
.intro { color: var(--muted); font-size: 14px; margin: 0 0 28px; }
form > label { display: block; font-size: 13px; font-weight: 500; margin-bottom: 8px; }
form > input { display: block; width: 100%; margin-bottom: 20px; }
.token-hint { font-size: 12px; color: var(--muted); line-height: 1.7; margin: -6px 0 20px; }
form > .demo-credentials { display: flex; gap: 8px; align-items: flex-start; font-weight: 400; font-size: 12px; color: var(--muted); line-height: 1.6; margin-bottom: 22px; }
.demo-credentials input { accent-color: var(--accent); margin: 3px 0 0; flex-shrink: 0; }
form > button { width: 100%; padding: 12px 16px; font-size: 15px; display: flex; justify-content: space-between; align-items: center; border-radius: 10px; }
.login-error { color: var(--danger); font-size: 13px; line-height: 1.5; }
footer { display: flex; justify-content: space-between; font-size: 11px; color: var(--muted); margin-top: 26px; }
@media (max-width: 480px) { .login-card { padding: 26px 22px; border-radius: 20px; } h1 { font-size: 24px; } }
</style>
