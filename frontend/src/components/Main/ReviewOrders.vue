<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import {
  AlertCircle, ChevronDown, ChevronUp, Clock, User, Check, X,
  ClipboardList, FileText, Inbox, Trash2, PencilLine, ArrowRight, Undo2, ListFilter
} from 'lucide-vue-next';
import type { ReviewOrder, ReviewOrderFields } from '../../types';

const props = defineProps<{
  items: ReviewOrder[];
  targetDate?: string;
  busy?: boolean;
}>();

const emit = defineEmits<{
  (e: 'promote', ids: string[]): void;
  (e: 'discard', ids: string[]): void;
  (e: 'openActionRules'): void;
}>();

// 订单信息默认收起；只展开用户主动点开的那几条。
const expanded = ref<Record<string, boolean>>({});
const allExpanded = computed(() =>
  props.items.length > 0 && props.items.every(i => expanded.value[i.id])
);
const actionCount = computed(() => props.items.filter(i => i.kind === 'action').length);

const toggle = (id: string) => {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] };
};
const toggleAll = () => {
  const next: Record<string, boolean> = {};
  if (!allExpanded.value) {
    props.items.forEach(i => { next[i.id] = true; });
  }
  expanded.value = next;
};

// 新一轮识别会替换整个列表，顺手清掉上一轮的展开状态，避免状态残留。
watch(() => props.items.map(i => i.id).join('|'), () => { expanded.value = {}; });

const isAction = (item: ReviewOrder) => item.kind === 'action';
const primary = (item: ReviewOrder) => item.orders?.[0] || {};
const summary = (item: ReviewOrder) => {
  if (isAction(item)) {
    const o = item.before?.[0] || item.after?.[0] || {};
    return [o['收件人'], o['商品规格']].filter(Boolean).join(' · ') || item.changes || '订单操作';
  }
  const o = primary(item);
  return [o['收件人'], o['手机号'], o['商品规格']].filter(Boolean).join(' · ') || '未提取到订单字段';
};
const confidenceText = (c: string) => (
  c === 'high' ? '高置信度' : c === 'low' ? '低置信度' : c === 'medium' ? '中置信度' : '未标注'
);
const actionLabel = (item: ReviewOrder) => item.actionType === 'delete' ? '删除订单' : '修改订单';
const statusLabel = (item: ReviewOrder) => {
  const map: Record<string, string> = {
    applied: '已定位并处理', manual: '需人工补充', ambiguous: '定位有歧义', unmatched: '未定位到订单'
  };
  return map[item.actionStatus || ''] || '待处理';
};
// 订单操作项复用原有 promote / discard 通道：
// 加入 = 采用操作结果（删除即不加单），忽略 = 撤销操作并恢复原始订单。
const promoteLabel = (item: ReviewOrder) => {
  if (!isAction(item)) return '加入预览区';
  if (item.actionStatus === 'applied') return item.actionType === 'delete' ? '确认删除' : '采用修改';
  return '已知悉';
};
const discardLabel = (item: ReviewOrder) => {
  if (!isAction(item)) return '忽略';
  if (item.actionType === 'delete') return '恢复订单';
  return '保留原单';
};
const actionColumns = (item: ReviewOrder) => ([
  { label: '操作前 · 原始订单', list: (item.before || []) as ReviewOrderFields[] },
  { label: '操作后 · 采用后的结果', list: (item.after || []) as ReviewOrderFields[] }
]);
</script>

<template>
  <section class="surface-card p-5 space-y-4">
    <!-- 标题区 -->
    <div class="flex items-start justify-between gap-4 flex-wrap">
      <div class="flex items-start gap-3 min-w-0">
        <div class="w-9 h-9 rounded-lg bg-warning-soft border border-warning-border flex items-center justify-center shrink-0">
          <ClipboardList class="w-4 h-4 text-warning-ink" />
        </div>
        <div class="min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <h2 class="text-ink font-semibold text-base">待人工审查 · 不确认数据</h2>
            <span class="px-2 py-0.5 rounded bg-warning-soft text-warning-ink text-[10px] font-bold">
              {{ items.length }} 条
            </span>
            <span v-if="actionCount" class="px-2 py-0.5 rounded bg-brand-soft border border-brand-border text-brand-ink text-[10px] font-bold">
              订单操作 {{ actionCount }} 条
            </span>
            <span v-if="targetDate" class="text-[11px] text-ink-3">目标订单日期 {{ targetDate }}</span>
          </div>
          <p class="text-xs text-ink-3 mt-1 leading-relaxed">
            存疑订单，以及客户后续要求「删除 / 修改」的订单都会停在这里，<b class="text-ink-2">不会自动进入数据预览区</b>。
            订单操作项给出原始与处理后的对照：点「采用修改 / 确认删除」接受处理，点「保留原单 / 恢复订单」撤销操作。
          </p>
        </div>
      </div>
      <div class="flex items-center gap-2 shrink-0 flex-wrap">
        <template v-if="items.length">
          <button
            @click="toggleAll"
            class="px-3 py-1.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs font-medium flex items-center gap-1.5 transition-all"
          >
            <ChevronUp v-if="allExpanded" class="w-3.5 h-3.5" />
            <ChevronDown v-else class="w-3.5 h-3.5" />
            {{ allExpanded ? '全部收起' : '全部展开' }}
          </button>
          <button
            @click="emit('promote', [])"
            :disabled="busy"
            class="px-3 py-1.5 rounded-md bg-brand-soft hover:opacity-90 border border-brand-border text-brand-ink text-xs font-bold flex items-center gap-1.5 transition-all disabled:opacity-50"
            title="普通订单加入数据预览区；订单操作项则采用处理结果（确认删除 / 采用修改）"
          >
            <Check class="w-3.5 h-3.5" /> 全部加入预览区
          </button>
        </template>
        <button
          @click="emit('openActionRules')"
          class="px-3 py-1.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs font-medium flex items-center gap-1.5 transition-all"
          title="配置群内口语对应的订单操作，例如「消单」= 删除订单"
        >
          <ListFilter class="w-3.5 h-3.5" /> 操作术语
        </button>
      </div>
    </div>

    <!-- 空状态 -->
    <div v-if="!items.length" class="flex items-center gap-2 px-4 py-3 rounded-md bg-subtle border border-line text-xs text-ink-3">
      <Inbox class="w-4 h-4 shrink-0" />
      本轮没有需要人工审查的数据；所有确认订单已在下方数据预览区。
    </div>

    <!-- 审查列表：默认收起 -->
    <div v-else class="space-y-2 max-h-[420px] overflow-y-auto custom-scrollbar pr-1">
      <div
        v-for="item in items"
        :key="item.id"
        :class="['rounded-lg border overflow-hidden', isAction(item) ? 'border-brand-border bg-brand-soft/30' : 'border-line bg-subtle/60']"
      >
        <!-- 收起时也能看到发送时间等关键判断依据 -->
        <div class="flex items-center gap-3 px-4 py-3">
          <button
            @click="toggle(item.id)"
            class="w-6 h-6 shrink-0 flex items-center justify-center rounded text-ink-3 hover:bg-hover hover:text-ink transition-colors"
            :title="expanded[item.id] ? '收起订单信息' : '展开订单信息'"
          >
            <ChevronUp v-if="expanded[item.id]" class="w-4 h-4" />
            <ChevronDown v-else class="w-4 h-4" />
          </button>

          <button @click="toggle(item.id)" class="flex-1 min-w-0 text-left">
            <div class="flex items-center gap-2 flex-wrap">
              <span v-if="isAction(item)" class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded bg-brand-soft border border-brand-border text-brand-ink text-[10px] font-bold">
                <Trash2 v-if="item.actionType === 'delete'" class="w-3 h-3" />
                <PencilLine v-else class="w-3 h-3" />
                {{ actionLabel(item) }}
              </span>
              <span class="text-sm text-ink font-medium truncate max-w-[220px]">{{ summary(item) }}</span>
              <span v-if="item.orderCount > 1" class="px-1.5 py-0.5 rounded bg-brand-soft text-brand-ink text-[10px]">
                {{ item.orderCount }} 笔
              </span>
              <span v-if="isAction(item)" :class="['px-1.5 py-0.5 rounded text-[10px]', item.actionStatus === 'applied' ? 'bg-success-soft text-success-ink' : 'bg-danger-soft text-danger-ink']">
                {{ statusLabel(item) }}
              </span>
              <span v-else class="px-1.5 py-0.5 rounded bg-warning-soft text-warning-ink text-[10px]">
                {{ item.orderDate ? '日期 ' + item.orderDate : '未写日期' }}
              </span>
              <span v-if="isAction(item) && item.matchBy" class="text-[10px] text-ink-3">{{ item.matchBy }}</span>
            </div>
            <div class="flex items-center gap-3 mt-1 text-[11px] text-ink-3 flex-wrap">
              <span class="flex items-center gap-1"><Clock class="w-3 h-3" /> 发送 {{ item.messageTime || item.messageDate || '未知' }}</span>
              <span class="flex items-center gap-1"><User class="w-3 h-3" /> {{ item.sender || '未知发送人' }}</span>
              <span class="truncate max-w-[420px]">{{ isAction(item) && item.changes ? item.changes : item.reason }}</span>
            </div>
          </button>

          <div class="flex items-center gap-2 shrink-0">
            <button
              @click="emit('promote', [item.id])"
              :disabled="busy"
              :class="['px-3 py-1.5 rounded-md text-onbrand text-xs font-bold flex items-center gap-1.5 transition-all disabled:opacity-50', isAction(item) && item.actionType === 'delete' && item.actionStatus === 'applied' ? 'bg-danger hover:opacity-90' : 'bg-success hover:opacity-90']"
              :title="isAction(item) ? '采用这次订单操作的处理结果' : '确认无误，加入下方数据预览区'"
            >
              <Check class="w-3.5 h-3.5" /> {{ promoteLabel(item) }}
            </button>
            <button
              v-if="isAction(item)"
              @click="emit('discard', [item.id])"
              :disabled="busy"
              class="px-2.5 py-1.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs font-medium flex items-center gap-1 transition-all disabled:opacity-50"
              :title="'撤销这次操作，把原始订单放回数据预览区'"
            >
              <Undo2 class="w-3.5 h-3.5" /> {{ discardLabel(item) }}
            </button>
            <button
              v-else
              @click="emit('discard', [item.id])"
              :disabled="busy"
              class="px-2.5 py-1.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs transition-all disabled:opacity-50"
              title="忽略这条"
            >
              <X class="w-3.5 h-3.5" />
            </button>
          </div>
        </div>

        <!-- 展开：完整元信息 + 原始消息 + 提取字段 -->
        <div v-if="expanded[item.id]" class="border-t border-line px-4 py-4 space-y-4 bg-surface">
          <div class="grid grid-cols-2 lg:grid-cols-4 gap-3 text-xs">
            <div><span class="text-ink-3">发送时间</span><div class="text-ink-2 mt-0.5 font-mono">{{ item.messageTime || '-' }}</div></div>
            <div><span class="text-ink-3">发送人</span><div class="text-ink-2 mt-0.5">{{ item.sender || '-' }}</div></div>
            <template v-if="isAction(item)">
              <div><span class="text-ink-3">操作类型</span><div class="text-ink-2 mt-0.5">{{ actionLabel(item) }} · {{ statusLabel(item) }}</div></div>
              <div><span class="text-ink-3">定位依据</span><div class="text-ink-2 mt-0.5">{{ item.matchBy || '未定位' }}</div></div>
            </template>
            <template v-else>
              <div><span class="text-ink-3">订单日期标记</span><div class="text-ink-2 mt-0.5">{{ item.orderDate || '未写（需人工确认）' }}</div></div>
              <div><span class="text-ink-3">AI 置信度</span><div class="text-ink-2 mt-0.5">{{ confidenceText(item.confidence) }}</div></div>
            </template>
          </div>

          <div :class="['flex items-start gap-2 px-3 py-2 rounded-md border text-xs', isAction(item) ? 'bg-brand-soft border-brand-border text-brand-ink' : 'bg-warning-soft border-warning-border text-warning-ink']">
            <AlertCircle class="w-3.5 h-3.5 shrink-0 mt-0.5" />
            <span>{{ isAction(item) ? '处理依据' : '需审查原因' }}：{{ item.reason }}</span>
          </div>

          <!-- 订单操作：变更说明 + 原始 / 结果对照 -->
          <template v-if="isAction(item)">
            <div v-if="item.changes" class="flex items-start gap-2 px-3 py-2 rounded-md bg-success-soft border border-success-border text-xs text-success-ink">
              <ArrowRight class="w-3.5 h-3.5 shrink-0 mt-0.5" />
              <span>变更内容：{{ item.changes }}</span>
            </div>
            <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
              <div
                v-for="(col, ci) in actionColumns(item)"
                :key="ci"
                :class="['rounded-md border p-3', ci === 0 ? 'border-line bg-subtle' : 'border-success-border bg-success-soft']"
              >
                <div class="text-xs font-semibold text-ink-2 mb-2">{{ col.label }}</div>
                <div
                  v-for="(o, idx) in col.list"
                  :key="idx"
                  class="rounded-md border border-line bg-surface p-2.5 grid grid-cols-1 gap-y-1 text-xs mb-2 last:mb-0"
                >
                  <div><span class="text-ink-3">收件人：</span><span class="text-ink">{{ o['收件人'] || '-' }}</span></div>
                  <div><span class="text-ink-3">手机号：</span><span class="text-ink font-mono">{{ o['手机号'] || '-' }}</span></div>
                  <div><span class="text-ink-3">收货地址：</span><span class="text-ink break-all">{{ o['收货地址'] || '-' }}</span></div>
                  <div><span class="text-ink-3">商品规格：</span><span class="text-ink font-medium">{{ o['商品规格'] || '-' }}</span></div>
                  <div v-if="o['备注']"><span class="text-ink-3">备注：</span><span class="text-ink">{{ o['备注'] }}</span></div>
                </div>
                <p v-if="!col.list.length" :class="['text-xs', ci === 1 && item.actionType === 'delete' ? 'text-danger-ink font-medium' : 'text-ink-3']">
                  {{ ci === 1 && item.actionType === 'delete' ? '此单将被移除（不会进入数据预览区）' : '没有订单字段' }}
                </p>
              </div>
            </div>
            <div v-if="item.candidates?.length" class="rounded-md border border-warning-border bg-warning-soft p-3">
              <div class="flex items-center gap-1.5 text-xs font-semibold text-warning-ink mb-2">
                <ListFilter class="w-3.5 h-3.5" /> 可能的候选订单（未自动改动任何订单）
              </div>
              <ul class="space-y-1 text-xs text-ink-2 list-disc list-inside">
                <li v-for="(c, ci) in item.candidates" :key="ci">{{ c }}</li>
              </ul>
            </div>
          </template>

          <div>
            <div class="flex items-center gap-1.5 text-xs font-semibold text-ink-2 mb-2">
              <FileText class="w-3.5 h-3.5" /> {{ isAction(item) ? '客户操作消息 / 引用的订单原文' : '原始聊天消息' }}
            </div>
            <pre class="whitespace-pre-wrap break-words font-mono text-xs text-ink-2 bg-subtle border border-line rounded-md p-3 max-h-52 overflow-y-auto custom-scrollbar leading-relaxed">{{ item.rawContent || '（空）' }}</pre>
          </div>

          <div v-if="!isAction(item)">
            <div class="flex items-center gap-1.5 text-xs font-semibold text-ink-2 mb-2">
              <ClipboardList class="w-3.5 h-3.5" /> AI 提取的订单字段（{{ item.orders?.length || 0 }} 笔）
            </div>
            <div class="space-y-2">
              <div
                v-for="(o, idx) in (item.orders || [])"
                :key="idx"
                class="rounded-md border border-line bg-subtle p-3 grid grid-cols-1 lg:grid-cols-2 gap-x-4 gap-y-1.5 text-xs"
              >
                <div><span class="text-ink-3">收件人：</span><span class="text-ink">{{ o['收件人'] || '-' }}</span></div>
                <div><span class="text-ink-3">手机号：</span><span class="text-ink font-mono">{{ o['手机号'] || '-' }}</span></div>
                <div class="lg:col-span-2"><span class="text-ink-3">收货地址：</span><span class="text-ink break-all">{{ o['收货地址'] || '-' }}</span></div>
                <div><span class="text-ink-3">商品规格：</span><span class="text-ink">{{ o['商品规格'] || '-' }}</span></div>
                <div><span class="text-ink-3">备注：</span><span class="text-ink">{{ o['备注'] || '-' }}</span></div>
              </div>
              <p v-if="!item.orders?.length" class="text-xs text-ink-3">没有提取到订单字段。</p>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
