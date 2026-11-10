<script setup lang="ts">
import { ChevronRight, KeyRound, LogOut } from 'lucide-vue-next';
import UpdateIndicator from './UpdateIndicator.vue';

defineProps<{
  userName: string;
  userRole: string;
  // 免登录（访客）模式下隐藏「修改密码 / 退出登录」，因为没有真实账号
  guest?: boolean;
}>();

defineEmits<{
  (e: 'logout'): void;
  (e: 'change-password'): void;
}>();
</script>

<template>
  <header class="h-14 border-b border-line flex items-center justify-between px-8 bg-surface shrink-0">
    <div class="flex items-center gap-4 text-sm text-ink-3">
      <span class="hover:text-ink transition-colors cursor-pointer">工作区</span>
      <ChevronRight class="w-4 h-4" />
      <span class="text-ink">订单智能识别</span>
    </div>
    <div class="flex items-center gap-2 lg:gap-3">
      <!-- 版本更新状态区（含更新日志入口）：版本标识本身常驻侧栏底部，顶栏不重复展示 -->
      <UpdateIndicator />

      <div class="flex items-center gap-3 pl-4 lg:pl-6 border-l border-line">
        <div class="hidden sm:block text-right">
          <p class="text-sm font-medium text-ink leading-tight">{{ userName }}</p>
          <p class="text-xs text-ink-3 leading-tight mt-0.5">{{ userRole }}</p>
        </div>
        <div class="w-9 h-9 rounded-full bg-brand-soft text-brand-ink flex items-center justify-center font-medium shrink-0">
          {{ (userName || 'U').charAt(0).toUpperCase() }}
        </div>
      </div>
      <!-- 免登录（访客）模式：没有数据库账号，改密 / 退出都无意义，直接隐藏 -->
      <span v-if="guest" class="hidden sm:inline-flex h-8 items-center px-2.5 rounded-md bg-subtle border border-line text-xs text-ink-3">
        免登录模式
      </span>
      <button
        v-if="!guest"
        @click="$emit('change-password')"
        class="h-9 px-2 lg:px-3 rounded-md flex items-center gap-2 text-xs font-medium text-ink-2 hover:text-ink hover:bg-hover border border-transparent hover:border-line transition-colors"
        title="修改当前账号密码"
      >
        <KeyRound class="w-4 h-4" />
        <span class="hidden lg:inline">修改密码</span>
      </button>
      <button
        v-if="!guest"
        @click="$emit('logout')"
        class="h-9 px-2 lg:px-3 rounded-md flex items-center gap-2 text-xs font-medium text-ink-2 hover:text-danger hover:bg-danger-soft border border-transparent hover:border-danger-border transition-colors"
        title="退出登录"
      >
        <LogOut class="w-4 h-4" />
        <span class="hidden lg:inline">退出</span>
      </button>
    </div>
  </header>
</template>
