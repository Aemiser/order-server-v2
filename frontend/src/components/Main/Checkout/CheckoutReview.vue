<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import {
  AlertCircle, ChevronDown, ChevronUp, Clock, User, Check, X,
  ClipboardList, FileText, Inbox, ShieldCheck, Loader2, ArrowRight, ListFilter
} from 'lucide-vue-next';

interface AuditOrderRow {
  '收件人'?: string;
  '手机号'?: string;
  '收货地址'?: string;
  '商品规格'?: string;
  '备注'?: string;
  createdAt?: string;
  VID?: number;
}

interface AuditItem {
  id: string;
  kind: string;          // missing 漏单 / uncertain 核对存疑 / review 识别存疑
  kindLabel: string;
  reason: string;
  confidence: string;
  sender: string;
  messageTime: string;
  messageDate: string;
  orderDate: string;
  rawContent: string;
  orderCount: number;
  orders: AuditOrderRow[];
  matchScore: number;
  matchLabel: string;
  matchedAt: string;
  matchOrders: AuditOrderRow[];
}

interface AuditSummary {
  success?: boolean;
  message?: string;
  from?: string;
  to?: string;
  exported?: number;
  candidates?: number;
  aiFailed?: number;
  checkoutOrders?: number;
  recognizedOrders?: number;
  matched?: number;
  missing?: number;
  uncertain?: number;
  unmatchedBills?: number;
  reviewCount?: number;
  warnings?: string[];
}

const props = defineProps<{
  items: AuditItem[];
  summary?: AuditSummary | null;
  busy?: boolean;
  progress?: string;
  error?: string;
}>();

const emit = defineEmits<{
  (e: 'promote', ids: string[]): void;
  (e: 'discard', ids: string[]): void;
}>();

const expanded = ref<Record<string, boolean>>({});
const allExpanded = computed(() =>
  props.items.length > 0 && props.items.every(i => expanded.value[i.id])
);
const toggle = (id: string) => {
  expanded.value = { ...expanded.value, [id]: !expanded.value[id] };
};
const toggleAll = () => {
  const next: Record<string, boolean> = {};
  if (!allExpanded.value) props.items.forEach(i => { next[i.id] = true; });
  expanded.value = next;
};
// 新一轮审核替换整个列表时清掉展开状态
watch(() => props.items.map(i => i.id).join('|'), () => { expanded.value = {}; });

const missingCount = computed(() => props.items.filter(i => i.kind === 'missing').length);
const uncertainCount = computed(() => props.items.filter(i => i.kind !== 'missing').length);
const primary = (item: AuditItem) => item.orders?.[0] || {};
const summaryLine = (item: AuditItem) => {
  const o = primary(item);
  return [o['收件人'], o['手机号'], o['商品规格']].filter(Boolean).join(' · ') || '未提取到订单字段';
};
const kindClass = (kind: string) => {
  if (kind === 'missing') return 'bg-danger-soft text-danger-ink';
  if (kind === 'uncertain') return 'bg-warning-soft text-warning-ink';
  return 'bg-brand-soft text-brand-ink';
};
const cardClass = (item: AuditItem) =>
  item.kind === 'missing' ? 'border-line bg-subtle/60' : 'border-line bg-subtle/60';
const stats = computed(() => {
  const s = props.summary || {};
  return [
    { label: '导出消息', value: s.exported ?? 0 },
    { label: '账单订单', value: s.checkoutOrders ?? 0 },
    { label: '识别订单', value: s.recognizedOrders ?? 0 },
    { label: '核对一致', value: s.matched ?? 0, ok: true },
    { label: '疑似漏单', value: s.missing ?? 0, warn: true },
    { label: '存疑订单', value: s.uncertain ?? 0, warn: true }
  ];
});
</script>

<template>
  <section class="surface-card p-5 space-y-4">
    <!-- 标题 -->
    <div class="flex items-start justify-between gap-4 flex-wrap">
      <div class="flex items-start gap-3 min-w-0">
        <div class="w-9 h-9 rounded-lg bg-warning-soft border border-warning-border flex items-center justify-center shrink-0">
          <ShieldCheck class="w-4 h-4 text-warning-ink" />
        </div>
        <div class="min-w-0">
          <div class="flex items-center gap-2 flex-wrap">
            <h2 class="text-ink font-semibold text-base">人工审核 · 账单核对</h2>
            <span class="px-2 py-0.5 rounded bg-warning-soft text-warning-ink text-[10px] font-bold">
              {{ items.length }} 条待处理
            </span>
            <span v-if="missingCount" class="px-2 py-0.5 rounded bg-danger-soft text-danger-ink text-[10px] font-bold">
              疑似漏单 {{ missingCount }}
            </span>
            <span v-if="uncertainCount" class="px-2 py-0.5 rounded bg-brand-soft border border-brand-border text-brand-ink text-[10px] font-bold">
              存疑 {{ uncertainCount }}
            </span>
            <span v-if="summary?.from" class="text-[11px] text-ink-3">
              审核范围 {{ summary.from }} ~ {{ summary.to }}
            </span>
          </div>
          <p class="text-xs text-ink-3 mt-1 leading-relaxed">
            已把所选日期范围内的群聊订单与账单订单逐笔核对，<b class="text-ink-2">核对一致的不会出现在这里</b>；
            只有疑似漏单和不确定订单需要人工判断。点「加入预览区」= 认定为漏单，订单会进入「订单输入」页的数据预览区，
            <b class="text-ink-2">仍不会自动入库</b>；补录后请回来重新查询账单。
          </p>
        </div>
      </div>
      <div class="flex items-center gap-2 shrink-0 flex-wrap" v-if="items.length">
        <button
          @click="toggleAll"
          class="px-3 py-1.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs font-medium flex items-center gap-1.5 transition-all"
        >
          <ChevronUp v-if="allExpanded" class="w-3.5 h-3.5" />
          <ChevronDown v-else class="w-3.5 h-3.5" />
          {{ allExpanded ? '全部收起' : '全部展开' }}
        </button>
        <button
          @click="emit('discard', [])"
          :disabled="busy"
          class="px-3 py-1.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs font-medium transition-all disabled:opacity-50"
        >
          全部忽略
        </button>
        <button
          @click="emit('promote', [])"
          :disabled="busy"
          class="px-3 py-1.5 rounded-md bg-brand-soft hover:opacity-90 border border-brand-border text-brand-ink text-xs font-bold flex items-center gap-1.5 transition-all disabled:opacity-50"
          title="把全部待审订单加入数据预览区（仍不入库）"
        >
          <Check class="w-3.5 h-3.5" /> 全部加入预览区
        </button>
      </div>
    </div>

    <!-- 汇总数字 -->
    <div v-if="summary" class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-2">
      <div
        v-for="s in stats"
        :key="s.label"
        class="rounded-md border border-line bg-subtle px-3 py-2"
      >
        <div class="text-[10px] text-ink-3">{{ s.label }}</div>
        <div
          class="text-lg font-semibold mt-0.5"
          :class="s.ok ? 'text-success-ink' : s.warn ? 'text-warning-ink' : 'text-ink'"
        >{{ s.value }}</div>
      </div>
    </div>

    <!-- 进度 / 错误 / 警告 -->
    <div v-if="busy" class="flex items-center gap-2 px-3 py-2 rounded-md bg-brand-soft border border-brand-border text-xs text-brand-ink">
      <Loader2 class="w-3.5 h-3.5 animate-spin shrink-0" />
      <span class="truncate">{{ progress || '正在导出群聊并识别订单…' }}</span>
    </div>
    <div v-if="error" class="flex items-start gap-2 px-3 py-2 rounded-md bg-danger-soft text-danger-ink text-xs">
      <AlertCircle class="w-3.5 h-3.5 shrink-0 mt-0.5" />
      <span>{{ error }}</span>
    </div>
    <div v-if="summary?.message && !busy" class="text-[11px] text-ink-3 px-1">
      {{ summary.message }}
    </div>
    <div v-if="summary?.warnings?.length" class="rounded-md border border-warning-border bg-warning-soft p-3">
      <div class="flex items-center gap-1.5 text-xs font-semibold text-warning-ink mb-1.5">
        <ListFilter class="w-3.5 h-3.5" /> 需要注意
      </div>
      <ul class="space-y-1 text-xs text-ink-2 list-disc list-inside">
        <li v-for="(w, i) in summary.warnings" :key="i">{{ w }}</li>
      </ul>
    </div>

    <!-- 空状态 -->
    <div v-if="!items.length && !busy" class="flex items-center gap-2 px-4 py-3 rounded-md bg-subtle border border-line text-xs text-ink-3">
      <Inbox class="w-4 h-4 shrink-0" />
      本轮核对没有发现漏单或存疑订单，无需人工处理。
    </div>

    <!-- 待审列表 -->
    <div v-else-if="items.length" class="space-y-2 max-h-[460px] overflow-y-auto custom-scrollbar pr-1">
      <div
        v-for="item in items"
        :key="item.id"
        :class="['rounded-lg border overflow-hidden', cardClass(item)]"
      >
        <div class="flex items-center gap-3 px-4 py-3">
          <button
            @click="toggle(item.id)"
            class="w-6 h-6 shrink-0 flex items-center justify-center rounded text-ink-3 hover:bg-hover hover:text-ink transition-colors"
            :title="expanded[item.id] ? '收起' : '展开对照详情'"
          >
            <ChevronUp v-if="expanded[item.id]" class="w-4 h-4" />
            <ChevronDown v-else class="w-4 h-4" />
          </button>

          <button @click="toggle(item.id)" class="flex-1 min-w-0 text-left">
            <div class="flex items-center gap-2 flex-wrap">
              <span class="px-1.5 py-0.5 rounded text-[10px] font-bold" :class="kindClass(item.kind)">
                {{ item.kindLabel || '待审核' }}
              </span>
              <span class="text-sm text-ink font-medium truncate max-w-[260px]">{{ summaryLine(item) }}</span>
              <span v-if="item.matchScore > 0" class="px-1.5 py-0.5 rounded bg-subtle border border-line text-ink-3 text-[10px]">
                相似度 {{ item.matchScore }}%
              </span>
              <span v-if="item.orderDate" class="text-[10px] text-ink-3">日期标记 {{ item.orderDate }}</span>
            </div>
            <div class="flex items-center gap-3 mt-1 text-[11px] text-ink-3 flex-wrap">
              <span class="flex items-center gap-1"><Clock class="w-3 h-3" /> 发送 {{ item.messageTime || item.messageDate || '未知' }}</span>
              <span class="flex items-center gap-1"><User class="w-3 h-3" /> {{ item.sender || '未知发送人' }}</span>
              <span class="truncate max-w-[420px]">{{ item.reason }}</span>
            </div>
          </button>

          <div class="flex items-center gap-2 shrink-0">
            <button
              @click="emit('promote', [item.id])"
              :disabled="busy"
              class="px-3 py-1.5 rounded-md bg-success hover:opacity-90 text-onbrand text-xs font-bold flex items-center gap-1.5 transition-all disabled:opacity-50"
              title="认定为漏单，加入数据预览区（仍需人工确认入库）"
            >
              <Check class="w-3.5 h-3.5" /> 加入预览区
            </button>
            <button
              @click="emit('discard', [item.id])"
              :disabled="busy"
              class="px-2.5 py-1.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs transition-all disabled:opacity-50"
              title="不是漏单，忽略这条"
            >
              <X class="w-3.5 h-3.5" />
            </button>
          </div>
        </div>

        <!-- 展开详情 -->
        <div v-if="expanded[item.id]" class="border-t border-line px-4 py-4 space-y-4 bg-surface">
          <div class="grid grid-cols-2 lg:grid-cols-4 gap-3 text-xs">
            <div><span class="text-ink-3">发送时间</span><div class="text-ink-2 mt-0.5 font-mono">{{ item.messageTime || '-' }}</div></div>
            <div><span class="text-ink-3">发送人</span><div class="text-ink-2 mt-0.5">{{ item.sender || '-' }}</div></div>
            <div><span class="text-ink-3">订单日期标记</span><div class="text-ink-2 mt-0.5">{{ item.orderDate || '未写（需人工确认）' }}</div></div>
            <div><span class="text-ink-3">分类 / 相似度</span><div class="text-ink-2 mt-0.5">{{ item.kindLabel }} · {{ item.matchScore }}%</div></div>
          </div>

          <div class="flex items-start gap-2 px-3 py-2 rounded-md border border-warning-border bg-warning-soft text-xs text-warning-ink">
            <AlertCircle class="w-3.5 h-3.5 shrink-0 mt-0.5" />
            <span>需审核原因：{{ item.reason }}</span>
          </div>

          <div>
            <div class="flex items-center gap-1.5 text-xs font-semibold text-ink-2 mb-2">
              <FileText class="w-3.5 h-3.5" /> 原始聊天消息
            </div>
            <pre class="whitespace-pre-wrap break-words font-mono text-xs text-ink-2 bg-subtle border border-line rounded-md p-3 max-h-52 overflow-y-auto custom-scrollbar leading-relaxed">{{ item.rawContent || '（空）' }}</pre>
          </div>

          <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
            <div class="rounded-md border border-line bg-subtle p-3">
              <div class="flex items-center gap-1.5 text-xs font-semibold text-ink-2 mb-2">
                <ClipboardList class="w-3.5 h-3.5" /> 群聊里识别出的订单（{{ item.orders?.length || 0 }} 笔）
              </div>
              <div v-for="(o, idx) in (item.orders || [])" :key="idx" class="rounded-md border border-line bg-surface p-2.5 grid grid-cols-1 gap-y-1 text-xs mb-2 last:mb-0">
                <div><span class="text-ink-3">收件人：</span><span class="text-ink">{{ o['收件人'] || '-' }}</span></div>
                <div><span class="text-ink-3">手机号：</span><span class="text-ink font-mono">{{ o['手机号'] || '-' }}</span></div>
                <div><span class="text-ink-3">收货地址：</span><span class="text-ink break-all">{{ o['收货地址'] || '-' }}</span></div>
                <div><span class="text-ink-3">商品规格：</span><span class="text-ink font-medium">{{ o['商品规格'] || '-' }}</span></div>
                <div v-if="o['备注']"><span class="text-ink-3">备注：</span><span class="text-ink">{{ o['备注'] }}</span></div>
              </div>
              <p v-if="!item.orders?.length" class="text-xs text-ink-3">没有提取到订单字段。</p>
            </div>

            <div class="rounded-md border border-line bg-subtle p-3">
              <div class="flex items-center gap-1.5 text-xs font-semibold text-ink-2 mb-2">
                <ArrowRight class="w-3.5 h-3.5" /> 疑似对应的账单订单
                <span v-if="item.matchedAt" class="font-normal text-ink-3">（入库 {{ item.matchedAt }}）</span>
              </div>
              <div v-for="(o, idx) in (item.matchOrders || [])" :key="idx" class="rounded-md border border-line bg-surface p-2.5 grid grid-cols-1 gap-y-1 text-xs mb-2 last:mb-0">
                <div><span class="text-ink-3">收件人：</span><span class="text-ink">{{ o['收件人'] || '-' }}</span></div>
                <div><span class="text-ink-3">手机号：</span><span class="text-ink font-mono">{{ o['手机号'] || '-' }}</span></div>
                <div><span class="text-ink-3">收货地址：</span><span class="text-ink break-all">{{ o['收货地址'] || '-' }}</span></div>
                <div><span class="text-ink-3">商品规格：</span><span class="text-ink font-medium">{{ o['商品规格'] || '-' }}</span></div>
              </div>
              <p v-if="!item.matchOrders?.length" class="text-xs text-ink-3">没有找到相似的账单订单，这条更可能是漏单。</p>
            </div>
          </div>
        </div>
      </div>
    </div>
  </section>
</template>
