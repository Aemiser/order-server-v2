<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import {
  Tags, X, Loader2, ArrowLeft, Plus, PencilLine, Trash2,
  ChevronRight, Info, Inbox,
} from 'lucide-vue-next';
import type { OrderActionRule } from '../../../types';
import {
  ACTION_OPTIONS, FIELD_OPTIONS, VALUE_RULE_OPTIONS, SCOPE_OPTIONS,
  emptyActionRule, listActionRules, saveActionRule, deleteActionRule,
  actionLabel, fieldLabel, valueRuleLabel, scopeLabel,
} from '../../../utils/action-rules';

// 一个弹窗三个视图：列表（默认）→ 详情 → 新增/编辑表单。
// 列表里长字段省略展示，悬浮出浮窗看全；点整行进详情，返回按钮回到刚才的列表。
const props = defineProps<{
  show: boolean;
  initialView?: 'list' | 'detail' | 'form';
  rule?: OrderActionRule | null;  // 直接定位到某条（配置页行点击用）
  prefill?: string;               // 人工审查区带进来的预填文本
  messages?: string[];            // 本轮审查消息，新增时可一键引用
}>();

const emit = defineEmits<{
  (e: 'close'): void;
  (e: 'saved'): void;
}>();

type View = 'list' | 'detail' | 'form';

const view = ref<View>('list');
const returnView = ref<View>('list');   // 表单取消 / 保存后回到哪
const rules = ref<OrderActionRule[]>([]);
const selected = ref<OrderActionRule | null>(null);
const formRule = ref<OrderActionRule | null>(null); // 非空 = 编辑模式
const loading = ref(true);   // 打开时先转圈，避免表格空状态闪一下
// 保存 / 删除都走乐观更新，点击即返回，不再需要表单级阻塞态。
const error = ref('');
const form = ref<OrderActionRule>(emptyActionRule());
const pickedMessage = ref('');

// 单元格级悬浮浮窗：悬浮到哪个字段，就只显示那一个字段的完整内容。
// Teleport 到 body 避免被弹窗的 overflow 裁掉。
type HoverCell = { label: string; value: string };
const hoverCell = ref<HoverCell | null>(null);
const hoverPos = ref({ x: 0, y: 0 });
const TOOLTIP_W = 300;
const TOOLTIP_H = 90;

const isEdit = computed(() => !!formRule.value?.id);
const currentAction = computed(() => ACTION_OPTIONS.find(o => o.value === form.value.action));
const needField = computed(() => form.value.action === 'modify');
const title = computed(() => {
  if (view.value === 'detail') return '术语详情';
  if (view.value === 'form') return isEdit.value ? '编辑订单操作术语' : '新增订单操作术语';
  return '订单操作术语 · 口语映射';
});
const subtitle = computed(() => {
  if (view.value === 'detail') return '这条规则的全部配置，以及它在识别链路里怎么生效。';
  if (view.value === 'form') return '把群里的口语 / 缩写映射到订单操作，保存后下一轮「一键识别订单」立即生效。';
  return '表格铺满弹窗、不横向滚动；长字段按列宽省略，悬浮某个字段只看它的完整内容，点整行看详情。';
});

const aliasList = (keyword: string) =>
  (keyword || '').split('/').map(s => s.trim()).filter(Boolean);

const shortScope = (v: string) =>
  ({ both: '全部', trigger: '仅筛选', prompt: '仅提示词' } as Record<string, string>)[v] || v || '—';

// 表格里用 CSS truncate 按列宽省略；浮窗里显示的就是这个未省略的原文。
const fieldValueText = (r: OrderActionRule) =>
  r.action === 'modify' ? `${fieldLabel(r.field)} · ${valueRuleLabel(r.valueRule)}` : '—';

// 详情页与悬浮浮窗共用同一份完整字段，避免两处口径不一致。
const detailRows = (r: OrderActionRule | null) => {
  if (!r) return [] as Array<{ label: string; value: string }>;
  return [
    { label: '触发关键词', value: r.keyword || '—' },
    { label: '同义别名', value: aliasList(r.keyword).join('、') || '—' },
    { label: '订单操作', value: actionLabel(r.action) },
    { label: '目标字段', value: r.action === 'modify' ? fieldLabel(r.field) : '—（作用于整笔订单）' },
    { label: '新值抽取', value: r.action === 'modify' ? valueRuleLabel(r.valueRule) : '—' },
    { label: '作用范围', value: scopeLabel(r.scope) },
    { label: '优先级', value: String(r.priority ?? 0) },
    { label: '启用状态', value: r.enabled ? '已启用' : '已停用' },
    { label: '显示顺序', value: String(r.sortOrder ?? 0) },
    { label: '备注', value: r.remark || '—' },
  ];
};

const ruleExplanation = (r: OrderActionRule | null) => {
  if (!r) return '';
  if (r.action === 'delete') {
    return `群里出现「${r.keyword}」时，即使大模型没认出来也会删除对应订单；店名 / 电话等定位线索由系统从已识别订单自动反查，不用配置。`;
  }
  if (r.action === 'ignore') {
    return `群里出现「${r.keyword}」时判为非操作，用来挡住疑问句、闲聊这类误伤；匹配词越长的规则优先级越高。`;
  }
  return `群里出现「${r.keyword}」时，把${fieldLabel(r.field)}按「${valueRuleLabel(r.valueRule)}」处理；抽不到新值会标低置信度交人工审核，绝不编造。`;
};

// 乐观删除期间后端还没真正删掉，任何一次后台刷新都要过滤掉这些 id，
// 否则「新增成功后刷新列表」会把刚删掉的行又拉回来。
const pendingDeleteIds = ref<number[]>([]);
let tempIdSeq = 0; // 同一毫秒内连续新增也能拿到不同临时 id
const sortedRules = (rows: OrderActionRule[]) =>
  [...rows].sort((a, b) => (a.sortOrder ?? 0) - (b.sortOrder ?? 0) || a.id - b.id);

// silent=true 用于乐观更新后的后台回读：数据要对齐，但不弹加载遮罩。
const loadRules = async (silent = false) => {
  if (!silent) loading.value = true;
  try {
    const rows = await listActionRules();
    rules.value = sortedRules(rows.filter(r => !pendingDeleteIds.value.includes(r.id)));
    const sid = selected.value?.id;
    if (sid) selected.value = rules.value.find(r => r.id === sid) || null;
    const fid = formRule.value?.id;
    if (fid) formRule.value = rules.value.find(r => r.id === fid) || null;
  } catch (e: any) {
    // 乐观更新后本地已经有数据，后台刷新失败不打断界面，更不能被误读成「保存失败」；
    // 只有首次打开、列表还空着时才提示。
    if (rules.value.length === 0) {
      error.value = '读取术语列表失败：' + (e?.message || e);
    }
  } finally {
    if (!silent) loading.value = false;
  }
};

const openForm = (r: OrderActionRule | null, from: View) => {
  hoverCell.value = null;
  formRule.value = r ? { ...r } : null;
  form.value = r ? { ...r } : { ...emptyActionRule(), keyword: (props.prefill || '').trim() };
  returnView.value = from;
  error.value = '';
  pickedMessage.value = '';
  view.value = 'form';
};

const openDetail = (r: OrderActionRule) => {
  hoverCell.value = null;
  selected.value = r;
  view.value = 'detail';
};

// 返回：表单回详情 / 列表，详情回列表，列表则关闭整个弹窗。
const goBack = () => {
  hoverCell.value = null;
  error.value = '';
  if (view.value === 'form') {
    view.value = returnView.value === 'detail' && selected.value ? 'detail' : 'list';
    return;
  }
  if (view.value === 'detail') {
    view.value = 'list';
    selected.value = null;
    return;
  }
  emit('close');
};

const closeAll = () => {
  hoverCell.value = null;
  emit('close');
};

watch(() => props.show, async (v) => {
  if (!v) return;
  error.value = '';
  hoverCell.value = null;
  await loadRules();
  const initial = props.initialView || 'list';
  if (initial === 'form') {
    openForm(props.rule || null, 'list');
  } else if (initial === 'detail' && props.rule) {
    selected.value = rules.value.find(r => r.id === props.rule!.id) || props.rule;
    returnView.value = 'list';
    view.value = 'detail';
  } else {
    view.value = 'list';
    selected.value = null;
    formRule.value = null;
  }
}, { immediate: true });

// 下拉只负责把原句带进输入框，最终关键词仍由用户裁剪，
// 避免把整条消息（含地址 / 电话）误当成触发词。
const applyReference = () => {
  if (pickedMessage.value) form.value.keyword = pickedMessage.value;
};

const moveHover = (e: MouseEvent) => {
  const x = Math.min(e.clientX + 16, window.innerWidth - TOOLTIP_W - 12);
  const y = Math.min(e.clientY + 16, window.innerHeight - TOOLTIP_H - 12);
  hoverPos.value = { x: Math.max(12, x), y: Math.max(12, y) };
};
// 只带当前这一个字段的 label + 完整值，不再把整条规则塞进浮窗。
const cellHover = (e: MouseEvent, label: string, value: string) => {
  hoverCell.value = { label, value: value || '—' };
  moveHover(e);
};
const leaveHover = () => { hoverCell.value = null; };

// 启用开关也走乐观更新：立刻改本地状态、后台落库。
// 刻意不调用 loadRules()——它会把 loading 置 true 并多渲染一行表格，
// 弹窗居中，内容高度一变就会上下抖动一下；乐观更新下 DOM 行数完全不变。
const toggleEnabled = (r: OrderActionRule) => {
  if (r.id < 0) return; // 乐观新增的行还没拿到数据库真实 id
  const current = rules.value.find(x => x.id === r.id);
  const next = !(current?.enabled ?? r.enabled);
  rules.value = rules.value.map(x => x.id === r.id ? { ...x, enabled: next } : x);
  error.value = '';
  void (async () => {
    try {
      const msg = await saveActionRule({ ...r, enabled: next });
      if (msg) throw new Error(msg);
      emit('saved');
    } catch (e: any) {
      rules.value = rules.value.map(x => x.id === r.id ? { ...x, enabled: !next } : x);
      error.value = '切换启用状态失败，已恢复：' + (e?.message || e);
    }
  })();
};

// 乐观删除：点确认立刻从本地列表移除并回到列表页，数据库删除放后台跑。
// 失败则把这一行放回原位并提示，用户可以重新操作。
const handleDelete = (r: OrderActionRule | null) => {
  if (!r || r.id < 0 || !window.confirm(`确定删除术语「${r.keyword}」吗？`)) return;
  const previous = rules.value.slice();
  rules.value = rules.value.filter(x => x.id !== r.id);
  pendingDeleteIds.value = [...pendingDeleteIds.value, r.id];
  selected.value = null;
  hoverCell.value = null;
  view.value = 'list';
  error.value = '';

  void (async () => {
    try {
      const msg = await deleteActionRule(r.id);
      if (msg) throw new Error(msg);
      emit('saved');
    } catch (e: any) {
      rules.value = previous;
      error.value = '删除失败，已恢复该术语：' + (e?.message || e);
    } finally {
      pendingDeleteIds.value = pendingDeleteIds.value.filter(id => id !== r.id);
    }
  })();
};

// 乐观保存：校验通过就立刻把结果写进本地列表并回到来源视图，后端落库在后台完成。
// 新增行先用负 id 占位（列表标「同步中」），成功后回读一次换成数据库真实 id。
const handleSubmit = () => {
  form.value.keyword = (form.value.keyword || '').trim();
  if (!form.value.keyword) { error.value = '关键词不能为空'; return; }
  if (needField.value && !form.value.field) { error.value = '修改订单必须选择目标字段'; return; }
  const payload: OrderActionRule = { ...form.value };
  if (!needField.value) { payload.field = ''; payload.valueRule = 'none'; }
  payload.remark = (payload.remark || '').trim();

  const previous = rules.value.slice();
  const previousSelected = selected.value;
  const previousFormRule = formRule.value;
  const isNew = !formRule.value?.id;
  const targetView: View = returnView.value === 'detail' && !isNew ? 'detail' : 'list';

  if (isNew) {
    rules.value = sortedRules([...rules.value, { ...payload, id: -Date.now() - (++tempIdSeq) }]);
    selected.value = null;
  } else {
    rules.value = sortedRules(rules.value.map(x => x.id === payload.id ? { ...payload } : x));
    selected.value = { ...payload };
  }
  hoverCell.value = null;
  error.value = '';
  view.value = targetView;
  if (targetView === 'list') selected.value = null;

  void (async () => {
    try {
      const msg = await saveActionRule(payload);
      if (msg) throw new Error(msg);
      emit('saved');
      if (isNew) await loadRules(true); // 静默拿真实 id；编辑的本地值已与提交值一致，不必回读
    } catch (e: any) {
      rules.value = previous;
      selected.value = previousSelected;
      formRule.value = previousFormRule;
      error.value = '保存失败，已撤销本次修改：' + (e?.message || e);
    }
  })();
};
</script>

<template>
  <div v-if="show" class="fixed inset-0 bg-overlay flex items-center justify-center z-50 p-4">
    <div :class="['bg-surface border border-line rounded-modal w-full max-h-[92vh] shadow-pop flex flex-col overflow-hidden transition-[max-width] duration-200', view === 'list' ? 'max-w-6xl h-[86vh]' : 'max-w-3xl']">

      <!-- 头部：视图切换时标题与按钮跟着变 -->
      <div class="flex items-start justify-between gap-4 px-7 pt-6 pb-4 border-b border-line shrink-0">
        <div class="flex items-start gap-3 min-w-0">
          <button v-if="view !== 'list'" @click="goBack"
                  class="w-9 h-9 rounded-lg bg-subtle hover:bg-hover border border-line flex items-center justify-center text-ink-2 shrink-0 transition-colors"
                  title="返回">
            <ArrowLeft class="w-4 h-4" />
          </button>
          <div class="w-9 h-9 rounded-lg bg-brand-soft border border-brand-border flex items-center justify-center shrink-0">
            <Tags class="w-4 h-4 text-brand-ink" />
          </div>
          <div class="min-w-0">
            <h3 class="text-lg font-bold text-ink">{{ title }}</h3>
            <p class="text-xs text-ink-3 mt-1 leading-relaxed">{{ subtitle }}</p>
          </div>
        </div>
        <div class="flex items-center gap-2 shrink-0">
          <button v-if="view === 'list'" @click="openForm(null, 'list')"
                  class="px-3.5 py-2 rounded-md bg-brand hover:bg-brand-hover text-onbrand text-xs font-bold flex items-center gap-1.5 transition-colors">
            <Plus class="w-3.5 h-3.5" /> 新增术语
          </button>
          <button @click="closeAll"
                  class="w-8 h-8 rounded-md flex items-center justify-center text-ink-3 hover:bg-hover hover:text-ink transition-colors"
                  title="关闭">
            <X class="w-4 h-4" />
          </button>
        </div>
      </div>

      <!-- 视图一：术语列表 -->
      <div v-if="view === 'list'" class="flex-1 min-h-0 overflow-y-auto custom-scrollbar px-7 py-5 space-y-4">
        <div v-if="messages?.length" class="px-4 py-3 rounded-lg bg-brand-soft/40 border border-brand-border flex items-start gap-2.5">
          <Info class="w-4 h-4 text-brand-ink shrink-0 mt-0.5" />
          <p class="text-xs text-ink-2 leading-relaxed">
            本轮有 {{ messages.length }} 条人工审查消息。点「新增术语」后，可从这些消息里选一句预填，再裁剪成触发词（例如「消单」）。
          </p>
        </div>

        <div class="rounded-lg border border-line overflow-hidden relative">
          <table class="w-full table-fixed text-xs" @mouseleave="leaveHover()">
            <colgroup>
              <col class="w-[22%]" />
              <col class="w-[11%]" />
              <col class="w-[23%]" />
              <col class="w-[10%]" />
              <col class="w-[7%]" />
              <col class="w-[10%]" />
              <col class="w-[13%]" />
              <col class="w-[4%]" />
            </colgroup>
            <thead class="bg-subtle text-ink-2" @mouseenter="leaveHover()">
              <tr>
                <th class="px-3 py-2.5 text-left font-medium">触发关键词</th>
                <th class="px-3 py-2.5 text-left font-medium">操作</th>
                <th class="px-3 py-2.5 text-left font-medium">字段 / 新值</th>
                <th class="px-3 py-2.5 text-left font-medium">范围</th>
                <th class="px-3 py-2.5 text-left font-medium">优先级</th>
                <th class="px-3 py-2.5 text-center font-medium">启用</th>
                <th class="px-3 py-2.5 text-left font-medium">备注</th>
                <th class="px-2 py-2.5"></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in rules" :key="r.id"
                  :class="['border-t border-line hover:bg-hover/50 transition-colors', r.id < 0 ? 'opacity-60 cursor-default' : 'cursor-pointer']"
                  @click="r.id >= 0 && openDetail(r)">
                <td class="px-3 py-2.5 overflow-hidden"
                    @mouseenter="cellHover($event, '触发关键词', r.keyword)" @mousemove="moveHover($event)">
                  <div class="truncate font-medium text-ink">
                    {{ r.keyword }}
                    <span v-if="r.id < 0" class="ml-1 px-1.5 py-0.5 rounded bg-subtle text-ink-3 text-[10px] font-normal">同步中</span>
                  </div>
                </td>
                <td class="px-3 py-2.5 overflow-hidden"
                    @mouseenter="cellHover($event, '订单操作', actionLabel(r.action))" @mousemove="moveHover($event)">
                  <div class="truncate">
                    <span :class="['px-2 py-0.5 rounded text-[11px] font-medium whitespace-nowrap inline-block',
                      r.action === 'delete' ? 'bg-danger-soft text-danger-ink' :
                      r.action === 'modify' ? 'bg-brand-soft text-brand-ink' : 'bg-subtle text-ink-2']">
                      {{ actionLabel(r.action) }}
                    </span>
                  </div>
                </td>
                <td class="px-3 py-2.5 overflow-hidden"
                    @mouseenter="cellHover($event, '字段 / 新值', fieldValueText(r))" @mousemove="moveHover($event)">
                  <div class="truncate text-ink-2">{{ fieldValueText(r) }}</div>
                </td>
                <td class="px-3 py-2.5 overflow-hidden"
                    @mouseenter="cellHover($event, '作用范围', scopeLabel(r.scope))" @mousemove="moveHover($event)">
                  <div class="truncate text-ink-3">{{ shortScope(r.scope) }}</div>
                </td>
                <td class="px-3 py-2.5 overflow-hidden"
                    @mouseenter="cellHover($event, '优先级', String(r.priority))" @mousemove="moveHover($event)">
                  <div class="truncate text-ink-2 font-mono">{{ r.priority }}</div>
                </td>
                <!-- 启用列整格是开关热区：@click.stop 拦住行点击，label 铺满这一格，
                     用户哪怕点偏到单元格空白处也只切换状态，绝不会误跳详情页。 -->
                <td class="p-0 overflow-hidden" @click.stop
                    @mouseenter="cellHover($event, '启用状态', r.enabled ? '已启用' : '已停用')" @mousemove="moveHover($event)">
                  <label :class="['flex items-center justify-center w-full min-h-[40px] transition-colors',
                    r.id < 0 ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:bg-hover/70']"
                         :title="r.id < 0 ? '同步中，暂不可切换' : '点击这一格切换启用状态'">
                    <input type="checkbox" :checked="r.enabled" :disabled="r.id < 0"
                           @change.stop="toggleEnabled(r)" class="w-4 h-4 accent-brand" />
                  </label>
                </td>
                <td class="px-3 py-2.5 overflow-hidden"
                    @mouseenter="cellHover($event, '备注', r.remark)" @mousemove="moveHover($event)">
                  <div class="truncate text-ink-3">{{ r.remark || '—' }}</div>
                </td>
                <td class="px-2 py-2.5 text-ink-3"><ChevronRight class="w-3.5 h-3.5" /></td>
              </tr>
              <tr v-if="!rules.length && !loading">
                <td colspan="8" class="px-4 py-10 text-center text-ink-3">
                  <Inbox class="w-5 h-5 mx-auto mb-2 opacity-60" />
                  还没有配置术语。点右上角「新增术语」，把「消单」映射到「删除订单」即可。
                </td>
              </tr>
            </tbody>
          </table>
          <!-- 加载遮罩绝对定位覆盖，不占表格行高，弹窗尺寸不会因刷新而跳一下 -->
          <div v-if="loading" class="absolute inset-0 flex items-center justify-center gap-2 bg-surface/85 text-ink-3 text-xs">
            <Loader2 class="w-4 h-4 animate-spin" /> 读取术语配置…
          </div>
        </div>

        <p class="text-xs text-ink-3 leading-relaxed">
          店名 / 收件人 / 电话这类<b class="text-ink-2">定位线索不用配置</b>：系统会从已识别订单自动反查
          （「华夏农业大学消单」少一个「店」字也能命中）。关键词只负责「这是什么操作」。
        </p>
        <p v-if="error" class="px-4 py-2.5 rounded-md bg-danger-soft border border-danger-border text-danger-ink text-xs">{{ error }}</p>
      </div>

      <!-- 视图二：术语详情 -->
      <div v-else-if="view === 'detail' && selected" class="flex-1 min-h-0 overflow-y-auto custom-scrollbar px-7 py-5 space-y-4">
        <div class="rounded-lg border border-brand-border bg-brand-soft/30 px-5 py-4 flex items-start justify-between gap-4">
          <div class="min-w-0">
            <div class="text-[11px] text-ink-3 mb-1">触发关键词（含别名）</div>
            <div class="text-lg font-bold text-ink break-all">{{ selected.keyword }}</div>
            <div class="flex flex-wrap gap-1.5 mt-2">
              <span v-for="a in aliasList(selected.keyword)" :key="a"
                    class="px-2 py-0.5 rounded bg-surface border border-line text-[11px] text-ink-2">{{ a }}</span>
            </div>
          </div>
          <div class="shrink-0 text-right space-y-1.5">
            <span :class="['px-2.5 py-1 rounded text-xs font-medium inline-block',
              selected.action === 'delete' ? 'bg-danger-soft text-danger-ink' :
              selected.action === 'modify' ? 'bg-brand-soft text-brand-ink' : 'bg-surface border border-line text-ink-2']">
              {{ actionLabel(selected.action) }}
            </span>
            <div :class="['text-[11px]', selected.enabled ? 'text-success-ink' : 'text-warning-ink']">
              {{ selected.enabled ? '● 已启用' : '○ 已停用' }}
            </div>
          </div>
        </div>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
          <div v-for="row in detailRows(selected)" :key="row.label"
               class="px-4 py-3 rounded-lg bg-subtle border border-line">
            <div class="text-[11px] text-ink-3 mb-1">{{ row.label }}</div>
            <div class="text-sm text-ink break-words">{{ row.value }}</div>
          </div>
        </div>

        <div class="px-4 py-3 rounded-lg bg-subtle border border-line flex items-start gap-2.5">
          <Info class="w-4 h-4 text-brand-ink shrink-0 mt-0.5" />
          <p class="text-xs text-ink-2 leading-relaxed">{{ ruleExplanation(selected) }}</p>
        </div>
        <p v-if="error" class="px-4 py-2.5 rounded-md bg-danger-soft border border-danger-border text-danger-ink text-xs">{{ error }}</p>
      </div>

      <!-- 视图三：新增 / 编辑表单 -->
      <div v-else class="flex-1 min-h-0 overflow-y-auto custom-scrollbar px-7 py-5 space-y-4">
        <div v-if="!isEdit && messages?.length" class="px-4 py-3 rounded-lg bg-subtle border border-line space-y-2">
          <label class="block text-xs font-medium text-ink-2">从本轮人工审查消息里选一句（可选）</label>
          <select v-model="pickedMessage" @change="applyReference"
                  class="w-full px-3 py-2 rounded-md bg-surface border border-line-strong text-ink text-xs focus:outline-none focus:border-brand">
            <option value="">不选择</option>
            <option v-for="(m, i) in messages" :key="i" :value="m">{{ m.length > 60 ? m.slice(0, 60) + '…' : m }}</option>
          </select>
          <p class="text-[11px] text-ink-3">选中后原句会填入下方关键词框，请把它裁剪成真正的触发词，例如「消单」。</p>
        </div>

        <div>
          <label class="block text-sm font-medium text-ink-2 mb-1.5">触发关键词 <span class="text-danger">*</span></label>
          <input v-model="form.keyword" type="text" placeholder="例：消单/销单/抹单"
                 class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand transition-colors" />
          <p class="text-[11px] text-ink-3 mt-1">多个同义词用 <code class="font-mono">/</code> 分隔；匹配时字数最多的词优先（如「消单了吗」压过「消单」）。</p>
        </div>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-ink-2 mb-1.5">对应订单操作</label>
            <select v-model="form.action"
                    class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand">
              <option v-for="o in ACTION_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
            <p class="text-[11px] text-ink-3 mt-1">{{ currentAction?.hint }}</p>
          </div>
          <div>
            <label class="block text-sm font-medium text-ink-2 mb-1.5">作用范围</label>
            <select v-model="form.scope"
                    class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand">
              <option v-for="o in SCOPE_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
            <p class="text-[11px] text-ink-3 mt-1">「全部生效」= 候选筛选 + 提示词 + 确定性兜底。</p>
          </div>
        </div>

        <div v-if="needField" class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-ink-2 mb-1.5">要修改的字段 <span class="text-danger">*</span></label>
            <select v-model="form.field"
                    class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand">
              <option value="">请选择</option>
              <option v-for="o in FIELD_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </div>
          <div>
            <label class="block text-sm font-medium text-ink-2 mb-1.5">新值怎么抽</label>
            <select v-model="form.valueRule"
                    class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand">
              <option v-for="o in VALUE_RULE_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
            <p class="text-[11px] text-ink-3 mt-1">抽不到新值时会标为低置信度，交人工审核，绝不编造。</p>
          </div>
        </div>

        <div class="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-ink-2 mb-1.5">优先级</label>
            <input v-model.number="form.priority" type="number" min="0" max="9999"
                   class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand" />
            <p class="text-[11px] text-ink-3 mt-1">同长度关键词冲突时取数值更大的规则。</p>
          </div>
          <div>
            <label class="block text-sm font-medium text-ink-2 mb-1.5">显示顺序</label>
            <input v-model.number="form.sortOrder" type="number" min="0" max="9999"
                   class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand" />
          </div>
        </div>

        <div>
          <label class="block text-sm font-medium text-ink-2 mb-1.5">备注（给自己看的说明）</label>
          <input v-model="form.remark" type="text" placeholder="例：群内口语，等于取消订单"
                 class="w-full px-3.5 py-2.5 rounded-md bg-surface border border-line-strong text-ink text-sm focus:outline-none focus:border-brand" />
        </div>

        <label class="flex items-center gap-2.5 text-sm text-ink-2 cursor-pointer select-none pt-1">
          <input v-model="form.enabled" type="checkbox" class="w-4 h-4 accent-brand" />
          启用这条规则
        </label>

        <div v-if="error" class="px-4 py-2.5 rounded-md bg-danger-soft border border-danger-border text-danger-ink text-xs">{{ error }}</div>
      </div>

      <!-- 底部 -->
      <div class="flex items-center justify-between gap-3 px-7 py-4 border-t border-line shrink-0">
        <p class="text-[11px] text-ink-3">
          {{ view === 'list' ? '共 ' + rules.length + ' 条规则 · 表格铺满弹窗，悬浮字段看完整内容，点击行查看详情' :
             view === 'detail' ? '规则存数据库 cfg_order_action_rules，改动会立即同步到识别链路' :
             '保存后下一轮「一键识别订单」立即生效，不用重启、不用改提示词' }}
        </p>
        <div class="flex items-center gap-2 shrink-0">
          <template v-if="view === 'list'">
            <button @click="closeAll" class="px-5 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover text-sm font-medium transition-colors">关闭</button>
          </template>
          <template v-else-if="view === 'detail' && selected">
            <button @click="handleDelete(selected)" class="px-3.5 py-2 rounded-md border border-line text-danger-ink hover:bg-danger-soft text-xs font-medium transition-colors flex items-center gap-1.5">
              <Trash2 class="w-3.5 h-3.5" /> 删除
            </button>
            <button @click="goBack" class="px-4 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover text-sm font-medium transition-colors flex items-center gap-1.5">
              <ArrowLeft class="w-3.5 h-3.5" /> 返回列表
            </button>
            <button @click="openForm(selected, 'detail')" class="px-4 py-2 rounded-md bg-brand hover:bg-brand-hover text-onbrand text-sm font-bold transition-colors flex items-center gap-1.5">
              <PencilLine class="w-3.5 h-3.5" /> 编辑
            </button>
          </template>
          <template v-else>
            <button @click="goBack" class="px-5 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover text-sm font-medium transition-colors">取消</button>
            <button @click="handleSubmit"
                    class="px-5 py-2 rounded-md bg-brand hover:bg-brand-hover text-onbrand text-sm font-bold transition-colors flex items-center gap-1.5">
              {{ isEdit ? '保存修改' : '新增规则' }}
            </button>
          </template>
        </div>
      </div>
    </div>
  </div>

  <!-- 单元格级浮窗：只显示当前悬浮的那一个字段的完整内容 -->
  <Teleport to="body">
    <div v-if="show && view === 'list' && hoverCell"
         class="fixed z-[60] pointer-events-none max-w-xs rounded-md border border-line bg-surface shadow-pop px-3 py-2 text-xs"
         :style="{ left: hoverPos.x + 'px', top: hoverPos.y + 'px' }">
      <div class="text-[11px] text-ink-3 mb-1">{{ hoverCell.label }}</div>
      <div class="text-ink break-words leading-relaxed">{{ hoverCell.value }}</div>
    </div>
  </Teleport>
</template>
