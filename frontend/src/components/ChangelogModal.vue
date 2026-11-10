<script setup lang="ts">
import { computed } from 'vue';
import { X, History, Loader2 } from 'lucide-vue-next';
import {
  changelogList,
  changelogLoading,
  changelogError,
  appVersion,
  versionLabel,
} from '../stores/update';
import { renderChangelogMarkdown } from '../utils/changelog-markdown';

defineEmits<{ (e: 'close'): void }>();

const hasData = computed(() => changelogList.value.length > 0);

// 把 tag 渲染成带 v 前缀的展示形式，与侧栏版本号一致
const displayTag = (tag: string) => (tag ? `v${tag}` : tag);
const isCurrent = (tag: string) => !!appVersion.value && tag === appVersion.value;
// 更新日志正文是 Markdown：### 分组、- 列表、> 说明、`代码`。
// 渲染器先做 HTML 转义再插入固定标签，输出可直接交给 v-html。
const renderBody = (body: string) => renderChangelogMarkdown(body);
</script>

<template>
  <div
    class="fixed inset-0 bg-overlay flex items-center justify-center z-[60] p-4"
    @click.self="$emit('close')"
  >
    <div class="bg-surface border border-line rounded-modal w-full max-w-3xl max-h-[82vh] flex flex-col shadow-[0_8px_40px_rgba(38,42,46,0.18)]">
      <!-- 弹窗头 -->
      <div class="flex items-center justify-between px-5 py-4 border-b border-line shrink-0">
        <div class="flex items-center gap-2 min-w-0">
          <History class="w-5 h-5 text-brand shrink-0" />
          <h3 class="text-base font-semibold text-ink truncate">版本更新历史</h3>
          <span class="text-xs text-ink-3 shrink-0">当前 {{ versionLabel }}</span>
        </div>
        <button
          type="button"
          @click="$emit('close')"
          class="w-8 h-8 rounded-md flex items-center justify-center text-ink-3 hover:text-ink hover:bg-hover transition-colors shrink-0"
          title="关闭"
        >
          <X class="w-4 h-4" />
        </button>
      </div>

      <!-- 弹窗体 -->
      <div class="overflow-y-auto custom-scrollbar p-5 flex-1">
        <!-- 加载中 -->
        <div v-if="changelogLoading" class="flex items-center justify-center gap-2 py-16 text-sm text-ink-3">
          <Loader2 class="w-4 h-4 animate-spin" />
          正在读取更新日志…
        </div>

        <!-- 出错 -->
        <div
          v-else-if="changelogError"
          class="py-12 text-center text-sm text-danger-ink"
        >
          {{ changelogError }}
        </div>

        <!-- 空态 -->
        <div v-else-if="!hasData" class="py-12 text-center text-sm text-ink-3">
          暂无更新日志
        </div>

        <!-- 历史列表 -->
        <div v-else class="space-y-5">
          <section
            v-for="rel in changelogList"
            :key="rel.tag"
            class="rounded-lg border p-4 transition-colors"
            :class="isCurrent(rel.tag)
              ? 'border-brand-border bg-brand-soft/40'
              : 'border-line bg-surface'"
          >
            <div class="flex items-center gap-2">
              <span class="font-mono font-semibold text-ink">{{ displayTag(rel.tag) }}</span>
              <span
                v-if="isCurrent(rel.tag)"
                class="inline-flex items-center px-1.5 py-0.5 rounded text-[11px] font-medium text-brand-ink bg-brand-soft border border-brand-border"
              >当前版本</span>
              <span class="text-xs text-ink-3 ml-auto shrink-0">{{ rel.published_at }}</span>
            </div>
            <h4
              v-if="rel.name"
              class="text-sm font-medium text-ink-2 mt-1"
            >{{ rel.name }}</h4>
            <div
              v-if="rel.body"
              class="changelog-md mt-2"
              v-html="renderBody(rel.body)"
            ></div>
            <div v-else class="text-sm text-ink-3 mt-2">（无更新说明）</div>
          </section>

          <p class="pt-2 text-xs text-ink-3 leading-relaxed">
            更新日志机制自 v10.0.9 起启用，更早版本发布时未逐条记录变更，故仅保留简要说明。
          </p>
        </div>
      </div>
    </div>
  </div>
</template>
