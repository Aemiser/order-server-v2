<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import OrderInput from './OrderInput.vue';
import OrderTable from './OrderTable.vue';
import ReviewOrders from './ReviewOrders.vue';
import type { Order, ReviewOrder } from '../../types';

const props = defineProps<{
  inputText: string;
  isAnalyzing: boolean;
  orders: Order[];
  reviewOrders: ReviewOrder[];
  targetDate?: string;
  reviewBusy?: boolean;
}>();

const emit = defineEmits<{
  (e: 'update:inputText', value: string): void;
  (e: 'clear'): void;
  (e: 'analyze', isAppend: boolean, modelId: number): void;
  (e: 'removeOrder', id: string): void;
  (e: 'clearAllOrders'): void;
  (e: 'refreshData'): void;
  (e: 'delete', id: string): void;
  (e: 'openSettings'): void;
  (e: 'promoteReview', ids: string[]): void;
  (e: 'discardReview', ids: string[]): void;
  (e: 'openActionRules'): void;
}>();

// 全屏状态检测
const isFullscreen = ref(false);
const isCollapsed = ref(false);

// 检测全屏状态
const checkFullscreen = () => {
  const fullscreenElement = document.fullscreenElement ||
                          (document as any).webkitFullscreenElement ||
                          (document as any).mozFullScreenElement ||
                          (document as any).msFullscreenElement;

  const wasFullscreen = isFullscreen.value;
  isFullscreen.value = !!fullscreenElement;

  // 当进入全屏时收缩区域，退出全屏时展开区域
  if (isFullscreen.value && !wasFullscreen) {
    isCollapsed.value = true;
  } else if (!isFullscreen.value && wasFullscreen) {
    isCollapsed.value = false;
  }
};

// 监听全屏变化事件
onMounted(() => {
  document.addEventListener('fullscreenchange', checkFullscreen);
  document.addEventListener('webkitfullscreenchange', checkFullscreen);
  document.addEventListener('mozfullscreenchange', checkFullscreen);
  document.addEventListener('MSFullscreenChange', checkFullscreen);
});

onUnmounted(() => {
  document.removeEventListener('fullscreenchange', checkFullscreen);
  document.removeEventListener('webkitfullscreenchange', checkFullscreen);
  document.removeEventListener('mozfullscreenchange', checkFullscreen);
  document.removeEventListener('MSFullscreenChange', checkFullscreen);
});
</script>

<template>
  <div class="space-y-6">
    <!-- 新建订单解析 卡片（标题与操作按钮由 OrderInput 提供） -->
    <section class="surface-card p-6">
      <div :class="['transition-all duration-500 ease-in-out', isCollapsed ? 'max-h-40 overflow-hidden' : 'max-h-none']">
        <OrderInput
          :inputText="inputText"
          :isAnalyzing="isAnalyzing"
          @update:inputText="$emit('update:inputText', $event)"
          @clear="$emit('clear')"
          @analyze="(a: boolean, b: number) => $emit('analyze', a, b)"
          @openSettings="$emit('openSettings')"
        />
      </div>
    </section>

    <!-- 不确认数据：人工审查区（订单信息默认收起，确认后才加入下方数据预览区） -->
    <ReviewOrders
      :items="reviewOrders"
      :targetDate="targetDate"
      :busy="reviewBusy"
      @promote="(ids: string[]) => $emit('promoteReview', ids)"
      @discard="(ids: string[]) => $emit('discardReview', ids)"
      @openActionRules="$emit('openActionRules')"
    />

    <!-- 数据预览 -->
    <div class="mt-6">
      <div class="mb-4 flex items-center gap-2">
        <h2 class="text-ink font-semibold text-base">数据预览</h2>
        <span class="px-2 py-0.5 rounded bg-brand-soft text-brand-ink text-[10px] font-bold uppercase tracking-wider">待导出</span>
      </div>
      <OrderTable
        :orders="orders"
        @remove="$emit('removeOrder', $event)"
        @clearAll="$emit('clearAllOrders')"
        @refreshData="$emit('refreshData')"
        @delete="$emit('delete', $event)"
      />
    </div>
  </div>
</template>

<style scoped>
/* 全屏状态下的样式调整 */
:deep(.order-input) {
  transition: all 0.5s ease-in-out;
}

:deep(.order-table) {
  transition: all 0.5s ease-in-out;
}
</style>
