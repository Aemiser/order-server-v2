<script setup lang="ts">
import { computed, onUnmounted, ref } from 'vue';
import { Box, FileText, Layers, Calculator, Settings, LayoutDashboard, PanelLeftClose, PanelLeftOpen, Shapes } from 'lucide-vue-next';
import { appVersion, versionLabel } from '../../stores/update';

defineProps<{
  activeTab: string;
  isSystemReady: boolean;
  statusMessage: string;
  collapsed: boolean;
}>();

defineEmits<{
  (e: 'update:activeTab', tab: string): void;
  (e: 'toggle-collapse'): void;
}>();

const navItems = [
  { key: 'input', label: '订单输入', icon: FileText },
  { key: 'details', label: '订单详情', icon: Layers },
  { key: 'checkout', label: '账单结算', icon: Calculator },
  { key: 'dashboard', label: '经营看板', icon: LayoutDashboard },
  { key: 'schema', label: '模板工作台', icon: Shapes },
  { key: 'settings', label: '应用设置', icon: Settings },
];

// 版本标识常驻侧栏底部：点击复制完整版本号，方便反馈问题时附上
const copied = ref(false);
let copyTimer: number | undefined;

const copyHint = computed(() => (
  copied.value
    ? '版本号已复制到剪贴板'
    : `OrderMaster ${versionLabel.value} · 点击复制版本号`
));

const copyVersion = async () => {
  if (!appVersion.value) return;
  const text = `OrderMaster v${appVersion.value}`;
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else {
      // Wails WebView 下 clipboard API 未必可用，退回传统复制方式
      const el = document.createElement('textarea');
      el.value = text;
      el.style.position = 'fixed';
      el.style.opacity = '0';
      document.body.appendChild(el);
      el.select();
      const ok = document.execCommand('copy');
      document.body.removeChild(el);
      if (!ok) return;
    }
  } catch {
    // 剪贴板被禁用时静默失败，不打扰用户
    return;
  }
  copied.value = true;
  window.clearTimeout(copyTimer);
  copyTimer = window.setTimeout(() => (copied.value = false), 1600);
};

onUnmounted(() => window.clearTimeout(copyTimer));
</script>

<template>
  <aside
    class="bg-sidebar border-r border-line flex flex-col justify-between py-5 h-full shrink-0 transition-[width] duration-200 ease-in-out"
    :class="collapsed ? 'w-[64px]' : 'w-56'"
  >
    <div :class="collapsed ? 'px-2' : 'px-4'">
      <!-- 品牌区：简洁模式只留图标 -->
      <div class="flex items-center gap-3 mb-8" :class="collapsed ? 'justify-center' : 'justify-start px-1'">
        <div class="w-8 h-8 bg-brand-soft rounded-lg flex items-center justify-center shrink-0">
          <Box class="w-5 h-5 text-brand-ink" />
        </div>
        <h1 v-if="!collapsed" class="text-lg font-bold tracking-tight text-ink whitespace-nowrap">OrderMaster</h1>
      </div>

      <nav class="space-y-1">
        <button
          v-for="item in navItems"
          :key="item.key"
          @click="$emit('update:activeTab', item.key)"
          :title="collapsed ? item.label : undefined"
          class="w-full flex items-center gap-3 rounded-md transition-colors duration-200 text-left"
          :class="[
            collapsed ? 'justify-center px-0 py-3' : 'justify-start px-4 py-3',
            activeTab === item.key ? 'bg-brand-soft text-brand-ink font-medium' : 'text-ink-2 hover:bg-hover hover:text-ink'
          ]"
        >
          <component :is="item.icon" class="w-5 h-5 shrink-0" />
          <span v-if="!collapsed" class="font-medium whitespace-nowrap">{{ item.label }}</span>
        </button>
      </nav>
    </div>

    <div :class="collapsed ? 'px-2' : 'px-4'">
      <!-- 系统状态：详细模式卡片 / 简洁模式指示点 -->
      <div v-if="!collapsed" class="p-3 rounded-lg bg-surface border border-line mb-4">
        <p class="text-xs text-brand-ink font-semibold mb-1">系统状态</p>
        <div class="flex items-center gap-2">
          <span class="w-2 h-2 rounded-full shrink-0" :class="isSystemReady ? 'bg-success' : 'bg-danger'"></span>
          <span class="text-xs text-ink-2">{{ statusMessage }}</span>
        </div>
      </div>
      <div v-else class="flex justify-center mb-4" :title="`系统状态：${statusMessage}`">
        <span class="w-2.5 h-2.5 rounded-full" :class="isSystemReady ? 'bg-success' : 'bg-danger'"></span>
      </div>

      <!-- 当前版本：常驻、跨页面可见；顶栏只负责「更新状态」 -->
      <button
        @click="copyVersion"
        type="button"
        :title="copyHint"
        class="w-full flex items-center mb-3 text-ink-3 hover:text-ink transition-colors cursor-pointer"
        :class="collapsed ? 'justify-center px-0 py-1.5 text-[11px] tracking-tight' : 'justify-between gap-2 px-2 py-1.5 text-xs'"
      >
        <span v-if="!collapsed" class="whitespace-nowrap">{{ copied ? '已复制版本号' : '当前版本' }}</span>
        <span class="font-mono whitespace-nowrap">{{ versionLabel }}</span>
      </button>

      <button
        @click="$emit('toggle-collapse')"
        :title="collapsed ? '展开侧栏' : '收起侧栏'"
        class="w-full flex items-center gap-3 rounded-md border border-line text-ink-3 hover:bg-hover hover:text-ink transition-colors text-xs"
        :class="collapsed ? 'justify-center px-0 py-2.5' : 'justify-start px-4 py-2.5'"
      >
        <PanelLeftClose v-if="!collapsed" class="w-4 h-4 shrink-0" />
        <PanelLeftOpen v-else class="w-4 h-4 shrink-0" />
        <span v-if="!collapsed" class="whitespace-nowrap">收起侧栏</span>
      </button>
    </div>
  </aside>
</template>
