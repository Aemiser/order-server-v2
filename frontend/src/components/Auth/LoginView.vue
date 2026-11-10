<script setup lang="ts">
import { ref } from 'vue';
import { Box, User, Lock, Eye, EyeOff, ShieldCheck, AlertCircle, Loader2 } from 'lucide-vue-next';
import { Login } from '../../../wailsjs/go/main/App';
import { versionLabel } from '../../stores/update';

const emit = defineEmits<{
  (e: 'login-success', state: { username: string; nickname: string; role: string }): void;
}>();

const username = ref('');
const password = ref('');
const showPassword = ref(false);
const loading = ref(false);
const error = ref('');

const submit = async () => {
  if (loading.value) return;
  error.value = '';

  const account = username.value.trim();
  if (!account || !password.value) {
    error.value = '请输入账号和密码';
    return;
  }

  loading.value = true;
  try {
    const res = await Login(account, password.value);
    if (res.ok && res.session) {
      emit('login-success', {
        username: res.session.username,
        nickname: res.session.nickname || res.session.username,
        role: res.session.role || 'operator',
      });
      password.value = '';
      error.value = '';
    } else {
      error.value = res.message || '登录失败，请重试';
      password.value = '';
    }
  } catch (e: any) {
    error.value = '登录请求失败：' + (e?.message || String(e));
  } finally {
    loading.value = false;
  }
};
</script>

<template>
  <div class="relative h-screen w-full overflow-hidden bg-[#0F1115] text-white font-sans flex items-center justify-center">
    <!-- 背景装饰 -->
    <div class="pointer-events-none absolute inset-0">
      <div class="absolute -top-40 -left-40 w-[520px] h-[520px] rounded-full bg-blue-600/10 blur-[120px]"></div>
      <div class="absolute -bottom-40 -right-32 w-[460px] h-[460px] rounded-full bg-teal-500/10 blur-[120px]"></div>
      <div class="absolute inset-0 opacity-[0.035]"
           style="background-image: linear-gradient(to right, white 1px, transparent 1px), linear-gradient(to bottom, white 1px, transparent 1px); background-size: 48px 48px;"></div>
    </div>

    <div class="relative w-full max-w-[420px] mx-4">
      <!-- 品牌 -->
      <div class="flex items-center justify-center gap-3 mb-8">
        <div class="w-11 h-11 bg-blue-600 rounded-xl flex items-center justify-center shadow-lg shadow-blue-600/30">
          <Box class="w-6 h-6 text-white" />
        </div>
        <div>
          <h1 class="text-2xl font-bold tracking-tight text-white">OrderMaster</h1>
          <p class="text-xs text-slate-500 tracking-widest uppercase">订单管理系统</p>
        </div>
      </div>

      <div class="bg-[#16191E]/90 backdrop-blur border border-white/10 rounded-2xl p-8 shadow-2xl shadow-black/40">
        <div class="mb-7">
          <h2 class="text-xl font-bold text-white">登录以继续</h2>
          <p class="text-sm text-slate-500 mt-1.5">请使用已授权的账号访问系统数据</p>
        </div>

        <form @submit.prevent="submit" class="space-y-5">
          <!-- 账号 -->
          <div>
            <label class="block text-xs font-medium text-slate-400 mb-2">账号</label>
            <div class="relative">
              <User class="absolute left-3.5 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-500" />
              <input
                v-model="username"
                type="text"
                autocomplete="username"
                autofocus
                placeholder="请输入账号"
                class="w-full h-11 pl-10 pr-4 rounded-xl bg-white/[0.03] border border-white/10 text-sm text-white placeholder:text-slate-600 outline-none transition-all focus:border-blue-500 focus:bg-white/[0.06]"
                @keyup.enter="submit"
              />
            </div>
          </div>

          <!-- 密码 -->
          <div>
            <label class="block text-xs font-medium text-slate-400 mb-2">密码</label>
            <div class="relative">
              <Lock class="absolute left-3.5 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-500" />
              <input
                v-model="password"
                :type="showPassword ? 'text' : 'password'"
                autocomplete="current-password"
                placeholder="请输入密码"
                class="w-full h-11 pl-10 pr-11 rounded-xl bg-white/[0.03] border border-white/10 text-sm text-white placeholder:text-slate-600 outline-none transition-all focus:border-blue-500 focus:bg-white/[0.06]"
                @keyup.enter="submit"
              />
              <button
                type="button"
                @click="showPassword = !showPassword"
                class="absolute right-3 top-1/2 -translate-y-1/2 text-slate-500 hover:text-slate-300 transition-colors"
                :aria-label="showPassword ? '隐藏密码' : '显示密码'"
              >
                <EyeOff v-if="showPassword" class="w-4 h-4" />
                <Eye v-else class="w-4 h-4" />
              </button>
            </div>
          </div>

          <!-- 错误提示 -->
          <div v-if="error" class="flex items-start gap-2 rounded-xl bg-red-500/10 border border-red-500/20 px-3.5 py-3">
            <AlertCircle class="w-4 h-4 text-red-400 shrink-0 mt-0.5" />
            <span class="text-xs text-red-300 leading-relaxed">{{ error }}</span>
          </div>

          <button
            type="submit"
            :disabled="loading"
            class="w-full h-11 rounded-xl bg-blue-600 hover:bg-blue-500 disabled:opacity-60 disabled:cursor-not-allowed text-white text-sm font-bold shadow-lg shadow-blue-600/25 transition-all flex items-center justify-center gap-2"
          >
            <Loader2 v-if="loading" class="w-4 h-4 animate-spin" />
            {{ loading ? '正在验证...' : '登 录' }}
          </button>
        </form>

        <div class="mt-6 pt-5 border-t border-white/5 flex items-center gap-2 text-xs text-slate-500">
          <ShieldCheck class="w-4 h-4 text-slate-600 shrink-0" />
          <span>连续 5 次验证失败将临时锁定 10 分钟</span>
        </div>
      </div>

      <p class="text-center text-xs text-slate-600 mt-6">仅限授权人员使用 · {{ versionLabel }}</p>
    </div>
  </div>
</template>
