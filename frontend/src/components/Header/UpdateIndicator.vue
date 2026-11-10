<script setup lang="ts">
import { computed, ref } from 'vue';
import { Loader2, Download, RefreshCw, AlertTriangle, Check, History, BookOpen } from 'lucide-vue-next';
import {
  updateState,
  versionLabel,
  currentChangelog,
  openChangelogModal,
} from '../../stores/update';
import { renderChangelogMarkdown } from '../../utils/changelog-markdown';
import { RestartApplication } from '../../../wailsjs/go/main/App';

// 本组件就是原来顶栏的「版本更新状态」区，不新增任何版本标识：
// 原状态项照常显示检查中 / 下载中 / 已就绪 / 更新失败 / 已是最新，
// 整块复用为更新日志入口 —— 悬停预览本次日志，点击查看版本历史。
const showRestartMenu = ref(false);
const toggleRestartMenu = () => { showRestartMenu.value = !showRestartMenu.value; };
const restartNow = () => {
  showRestartMenu.value = false;
  RestartApplication();
};
const openHistory = () => {
  showRestartMenu.value = false;
  openChangelogModal();
};

const historyHint = computed(() =>
  `当前 ${versionLabel.value} · 点击「版本更新历史」查看全部版本`,
);
const previewHtml = computed(() =>
  renderChangelogMarkdown(currentChangelog.value?.body || ''),
);
</script>

<template>
  <div class="relative group flex items-center">
    <!-- 悬停预览：本次版本更新日志。pt-2 让浮层与状态项之间的间隙也保持 hover，避免闪退 -->
    <div
      v-if="!showRestartMenu"
      class="absolute right-0 top-full z-50 w-[360px] pt-2 hidden group-hover:block"
    >
      <div class="max-h-72 overflow-auto custom-scrollbar bg-surface border border-line rounded-lg shadow-[0_8px_40px_rgba(38,42,46,0.18)] p-4">
        <div class="flex items-center justify-between gap-2 mb-2">
          <span class="text-xs font-semibold text-ink">{{ versionLabel }} · 本次更新日志</span>
          <span v-if="currentChangelog?.published_at" class="text-[11px] text-ink-3 shrink-0">
            {{ currentChangelog.published_at }}
          </span>
        </div>
        <div v-if="currentChangelog?.name" class="text-xs font-medium text-brand-ink mb-2">
          {{ currentChangelog.name }}
        </div>
        <!-- 与历史弹窗共用同一套 Markdown 渲染 -->
        <div
          v-if="currentChangelog?.body"
          class="changelog-md changelog-compact"
          v-html="previewHtml"
        ></div>
        <div v-else class="text-xs text-ink-3">
          暂无本次版本更新日志
        </div>
        <div class="mt-3 pt-2 border-t border-line text-[11px] text-ink-3">
          点击查看完整版本历史 →
        </div>
      </div>
    </div>

    <!-- 检查中 -->
    <button
      v-if="updateState.status === 'checking'"
      type="button"
      @click="openHistory"
      :title="historyHint"
      class="hidden sm:inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md text-xs text-ink-3 hover:text-ink hover:bg-hover transition-colors cursor-pointer"
    >
      <Loader2 class="w-3.5 h-3.5 animate-spin" />
      检查更新中
    </button>

    <!-- 下载中（内部有块级进度条，用可聚焦的 div 代替 button） -->
    <div
      v-else-if="updateState.status === 'downloading'"
      role="button"
      tabindex="0"
      @click="openHistory"
      @keydown.enter="openHistory"
      :title="historyHint"
      class="hidden sm:flex items-center gap-2 h-8 px-2.5 rounded-md bg-subtle border border-line hover:border-brand/40 transition-colors min-w-[170px] cursor-pointer"
    >
      <Download class="w-3.5 h-3.5 text-brand shrink-0" />
      <div class="flex-1 min-w-0">
        <div class="flex items-center justify-between text-[11px] text-ink-2 leading-none">
          <span class="truncate">新版本 {{ updateState.version }} 下载中</span>
          <span class="font-medium text-ink ml-1">{{ updateState.progress.toFixed(0) }}%</span>
        </div>
        <div class="w-full h-1 bg-line rounded-full mt-1 overflow-hidden">
          <div
            class="h-full bg-brand rounded-full transition-all duration-300"
            :style="{ width: updateState.progress + '%' }"
          ></div>
        </div>
      </div>
    </div>

    <!-- 已就绪：保留原有「立即重启 / 稍后」菜单，菜单内补一个历史入口 -->
    <div v-else-if="updateState.status === 'ready'" class="relative">
      <button
        type="button"
        @click="toggleRestartMenu"
        :title="historyHint"
        class="inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md bg-success-soft border border-success-border text-success-ink text-xs font-medium hover:opacity-80 transition-opacity cursor-pointer"
      >
        <RefreshCw class="w-3.5 h-3.5" />
        新版本 {{ updateState.version }} 已就绪 · 重启更新
      </button>
      <div
        v-if="showRestartMenu"
        class="absolute right-0 top-10 z-50 w-48 bg-surface border border-line rounded-md shadow-lg p-1.5 flex flex-col gap-1"
      >
        <button
          type="button"
          @click="restartNow"
          class="w-full text-left px-3 py-2 rounded-md text-sm text-ink hover:bg-hover flex items-center gap-2 transition-colors cursor-pointer"
        >
          <RefreshCw class="w-4 h-4 text-brand" />
          立即重启
        </button>
        <button
          type="button"
          @click="openHistory"
          class="w-full text-left px-3 py-2 rounded-md text-sm text-ink-2 hover:bg-hover flex items-center gap-2 transition-colors cursor-pointer"
        >
          <BookOpen class="w-4 h-4 text-ink-3" />
          查看版本更新历史
        </button>
        <button
          type="button"
          @click="showRestartMenu = false"
          class="w-full text-left px-3 py-2 rounded-md text-sm text-ink-2 hover:bg-hover transition-colors cursor-pointer"
        >
          稍后自己重启
        </button>
      </div>
    </div>

    <!-- 出错 -->
    <button
      v-else-if="updateState.status === 'error'"
      type="button"
      @click="openHistory"
      :title="updateState.errorMsg"
      class="hidden sm:inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md bg-danger-soft border border-danger-border text-danger-ink text-xs hover:opacity-80 transition-opacity cursor-pointer"
    >
      <AlertTriangle class="w-3.5 h-3.5" />
      更新失败
    </button>

    <!-- 已是最新：最常见的常驻状态，直接作为日志入口 -->
    <button
      v-else-if="updateState.status === 'uptodate' && updateState.currentVersion"
      type="button"
      @click="openHistory"
      :title="historyHint"
      class="hidden sm:inline-flex items-center gap-1.5 h-8 px-2.5 rounded-md text-xs text-ink-3 hover:text-ink hover:bg-hover transition-colors cursor-pointer"
    >
      <Check class="w-3.5 h-3.5 text-success" />
      已是最新
    </button>

    <!-- idle 兜底：后端启动即检查，此态几乎不可见，只留一个不显眼的图标入口 -->
    <button
      v-else
      type="button"
      @click="openHistory"
      :title="historyHint"
      class="hidden sm:inline-flex items-center gap-1.5 h-8 px-2 rounded-md text-xs text-ink-3 hover:text-ink hover:bg-hover transition-colors cursor-pointer"
    >
      <History class="w-3.5 h-3.5" />
      版本更新历史
    </button>
  </div>
</template>
