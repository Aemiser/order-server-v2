<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue';
import { Sparkles, Loader2, ChevronDown, ChevronUp, ChevronLeft, ChevronRight, CalendarDays, RotateCcw, MessageSquareMore, Settings2, CheckCircle2, AlertCircle } from 'lucide-vue-next';
import { recognitionState, startRecognition, today, dateStr } from '../../stores/recognition';

defineProps<{
  inputText: string;
  isAnalyzing: boolean;
}>();

// 模式选择：false 为"重来"，true 为"追加"
const isAppendMode = ref(false);

// AI模型选择
const aiModels = ref<Map<string, number>>(new Map());
const selectedModel = ref<string>('');
const isDropdownOpen = ref(false);
const isLoadingModels = ref(false);
const modelError = ref('');

// 一键识别订单：导出群聊 -> 可选粗筛 -> AI 识别 -> 结果填入下方数据预览区。
// 识别任务、进度与结果全部存放在常驻的 recognition store（见 stores/recognition.ts）：
// 切到其它页面时本组件会被销毁，但任务继续跑、进度继续收、结果照样回填数据预览区。
// 按钮不设禁用状态，可随时重复点击；Go 侧 runMu 会把多轮任务串行排队。
const isIncremental = computed(() => recognitionState.isIncremental);
const setIncremental = (v: boolean) => { recognitionState.isIncremental = v; };
const pendingRuns = computed(() => recognitionState.running);
const isFetching = computed(() => recognitionState.running > 0);
const fetchProgress = computed(() => recognitionState.progress);
const fetchSummary = computed(() => recognitionState.summary);
const fetchError = computed(() => recognitionState.error);
const showDetails = ref(false);
const showActions = ref(false);

// 要识别的订单日期：按次生效、不写配置文件，但放在 store 里跨页面保留。
const targetDate = computed(() => recognitionState.targetDate);
const isTargetToday = computed(() => targetDate.value === today());
const setTargetDate = (v: string) => { recognitionState.targetDate = v || today(); };
const onTargetDateChange = (e: Event) => setTargetDate((e.target as HTMLInputElement).value);

// 前后一天快捷切换，方便连续提取不同日期的订单；用本地时间构造，避免 UTC 时区偏移。
const shiftTargetDate = (delta: number) => {
  const parts = (recognitionState.targetDate || today()).split('-').map(Number);
  if (parts.length !== 3 || parts.some(n => !Number.isFinite(n))) {
    setTargetDate(today());
    return;
  }
  const next = dateStr(new Date(parts[0], parts[1] - 1, parts[2] + delta));
  // 不允许选到未来：导出层本来就会回退到今天，夹紧避免目标日期与导出窗口不一致
  setTargetDate(next > today() ? today() : next);
};
const resetTargetDate = () => setTargetDate(today());

const emit = defineEmits<{
  (e: 'update:inputText', value: string): void;
  (e: 'clear'): void;
  (e: 'analyze', isAppend: boolean, modelId: number): void;
  (e: 'openSettings'): void;
}>();

// 获取AI模型列表
const fetchAIModels = async () => {
  isLoadingModels.value = true;
  modelError.value = '';
  try {
    const models = await window['go']['main']['App']['GetAIModel']();
    aiModels.value = new Map(Object.entries(models));
    // 默认选择第一个模型
    if (aiModels.value.size > 0) {
      selectedModel.value = Array.from(aiModels.value.keys())[0];
    }
  } catch (error) {
    console.error('获取AI模型失败:', error);
    modelError.value = '获取模型列表失败，请重试';
  } finally {
    isLoadingModels.value = false;
  }
};

// 点击即发起一次识别，不 await：Go 侧任务在后台执行，结果由常驻 store 回填预览区，
// 本组件随后被销毁（切到其它页面）也不会中断识别或丢结果。
const fetchTodayOrders = () => {
  // 把按钮旁当前选的日期作为「本轮目标日期」、增量开关作为「识别范围」传给后端；均不落盘。
  const picked = recognitionState.targetDate || today();
  const runDate = picked > today() ? today() : picked;
  showDetails.value = false;
  showActions.value = false;
  startRecognition(runDate, recognitionState.isIncremental);
};

// 切换下拉菜单
const toggleDropdown = () => {
  isDropdownOpen.value = !isDropdownOpen.value;
};

// 选择模型
const selectModel = (model: string) => {
  selectedModel.value = model;
  isDropdownOpen.value = false;
};

// 处理点击外部关闭下拉菜单
const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement;
  if (!target.closest('.ai-model-selector')) {
    isDropdownOpen.value = false;
  }
};

// 进度事件由 App.vue（常驻根组件）统一订阅并写入 recognition store。
// 这里绝不能再订阅 / EventsOff：切页会连其它监听一起清掉，进度就断了。
onMounted(() => {
  fetchAIModels();
  document.addEventListener('click', handleClickOutside);
});

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside);
});
</script>

<template>
  <div class="space-y-4">
    <div class="flex items-start justify-between gap-4 flex-wrap">
      <div class="min-w-0">
    <h2 class="text-ink font-semibold text-lg">新建订单解析</h2>
    <p class="text-ink-3 text-sm mt-1">支持直接粘贴文本、拖入文件，或按右侧所选日期一键识别微信群订单与后续订单操作（确认订单进数据预览区，存疑订单与被操作过的订单进人工审查区）</p>
      </div>
      <div class="flex items-center gap-2 flex-wrap justify-end">
        <!-- 要识别的订单日期：放在按钮旁，改完直接点按钮即可，无需进配置页、无需保存 -->
        <div
          class="flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border transition-colors"
          :class="isTargetToday ? 'bg-subtle border-line' : 'bg-warning-soft border-warning-border'"
          title="每次启动默认今天。可用左右箭头切换相邻日期，或直接选历史日期；只对本次识别生效，不会写进配置文件。"
        >
          <CalendarDays class="w-4 h-4 shrink-0" :class="isTargetToday ? 'text-brand' : 'text-warning-ink'" />
          <span class="text-xs font-medium text-ink-2 whitespace-nowrap">要识别的订单日期</span>
          <button
            type="button"
            @click="shiftTargetDate(-1)"
            class="w-6 h-6 rounded flex items-center justify-center text-ink-2 hover:bg-hover transition-colors"
            title="前一天"
          >
            <ChevronLeft class="w-3.5 h-3.5" />
          </button>
          <input
            :value="targetDate"
            type="date"
            :max="today()"
            @change="onTargetDateChange"
            class="bg-surface border border-line-strong rounded px-2 py-1 text-sm text-ink outline-none focus:border-brand transition-colors"
            aria-label="要识别的订单日期"
          />
          <button
            type="button"
            @click="shiftTargetDate(1)"
            class="w-6 h-6 rounded flex items-center justify-center text-ink-2 hover:bg-hover transition-colors"
            title="后一天"
          >
            <ChevronRight class="w-3.5 h-3.5" />
          </button>
          <button
            type="button"
            @click="resetTargetDate"
            class="px-2 py-1 rounded text-xs font-medium flex items-center gap-1 transition-colors"
            :class="isTargetToday ? 'text-ink-3 hover:bg-hover' : 'text-brand-ink bg-brand-soft hover:opacity-80'"
            title="回到今天"
          >
            <RotateCcw class="w-3 h-3" /> 今天
          </button>
          <span v-if="!isTargetToday" class="text-xs text-warning-ink whitespace-nowrap">历史日期</span>
        </div>
        <button
          @click="emit('openSettings')"
          class="px-4 py-2.5 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-sm font-medium flex items-center gap-2 transition-all"
          title="配置群聊、顾客、导出天数、粗筛开关等"
        >
          <Settings2 class="w-4 h-4" /> 配置
        </button>
        <!-- 识别范围：全量 / 增量，默认增量。增量=只识别今天数据库里还没有的订单（按创建时间+订单内容判唯一） -->
        <div
          class="flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border border-line bg-subtle"
          title="全量：每次重新识别全部订单；增量（默认）：只识别「目标日期当天数据库里还没有」的订单，以创建时间（落在该日）+ 收件人/手机/地址/商品规格 判定唯一，避免重复识别已入库的订单"
        >
          <span class="text-xs font-medium text-ink-2 whitespace-nowrap">识别范围</span>
          <div class="inline-flex bg-surface rounded-md p-0.5">
            <button
              type="button"
              @click="setIncremental(false)"
              :class="!isIncremental ? 'bg-brand text-onbrand rounded shadow-sm' : 'text-ink-2'"
              class="px-3 py-1 text-sm rounded font-medium transition-colors"
            >全量</button>
            <button
              type="button"
              @click="setIncremental(true)"
              :class="isIncremental ? 'bg-brand text-onbrand rounded shadow-sm' : 'text-ink-2'"
              class="px-3 py-1 text-sm rounded font-medium transition-colors"
            >增量</button>
          </div>
        </div>
        <button
          @click="fetchTodayOrders"
          class="px-5 py-2.5 rounded-md bg-success hover:opacity-90 text-onbrand text-sm font-bold flex items-center gap-2 shadow-sm transition-all active:scale-95"
          title="按左侧所选的订单日期导出前 N 天群聊 → 可选粗筛 → AI 识别订单 → 识别并回放订单操作（删除/修改）；确认订单进数据预览区，存疑订单与被操作过的订单进人工审查区；不入库、不生成文件，可随时重复点击。默认增量模式：已存在于今日数据库的订单会被跳过"
        >
          <Loader2 v-if="isFetching" class="w-4 h-4 animate-spin" />
          <MessageSquareMore v-else class="w-4 h-4" />
          {{ isFetching ? (pendingRuns > 1 ? `正在识别…（${pendingRuns} 个任务）` : '正在识别…') : '一键识别订单' }}
        </button>
      </div>
    </div>
    <p v-if="!isTargetToday" class="text-xs text-warning-ink -mt-2">
      当前选择的是历史日期 {{ targetDate }}：将导出该日期前 N 天群聊，并只把日期标记为该日的订单放入数据预览区；想恢复默认请点「今天」。
    </p>

    <!-- 后台进度 -->
    <div v-if="isFetching" class="flex items-center gap-3 px-4 py-3 rounded-md bg-success-soft border border-success-border text-sm text-success-ink">
      <Loader2 class="w-4 h-4 animate-spin shrink-0" />
      <span class="truncate">{{ fetchProgress }}</span>
    </div>

    <!-- 出错提示 -->
    <div v-if="fetchError" class="flex items-start gap-3 px-4 py-3 rounded-md bg-danger-soft border border-danger-border text-sm text-red-200">
      <AlertCircle class="w-4 h-4 shrink-0 mt-0.5" />
      <span>{{ fetchError }}</span>
    </div>

    <!-- 结果摘要 -->
    <div v-if="fetchSummary" class="rounded-md bg-subtle border border-line p-4 space-y-3">
      <div class="flex items-start gap-3">
        <CheckCircle2 v-if="fetchSummary.success" class="w-4 h-4 text-success shrink-0 mt-0.5" />
        <AlertCircle v-else class="w-4 h-4 text-danger shrink-0 mt-0.5" />
        <div class="flex-1 min-w-0">
          <p class="text-sm text-ink">{{ fetchSummary.message }}</p>
          <p class="text-xs text-ink-3 mt-1">
            数据源 {{ fetchSummary.source || '-' }} · {{ fetchSummary.exportFrom }} ~ {{ fetchSummary.exportTo }}
            <span v-if="fetchSummary.targetDate"> · 目标订单日期 {{ fetchSummary.targetDate }}</span>
          </p>
        </div>
        <div class="flex items-center gap-3 shrink-0">
          <button
            v-if="fetchSummary.actions?.length"
            @click="showActions = !showActions"
            class="text-xs text-brand hover:text-brand flex items-center gap-1"
          >
            {{ showActions ? '收起订单操作' : '查看订单操作' }}
            <ChevronUp v-if="showActions" class="w-3 h-3" />
            <ChevronDown v-else class="w-3 h-3" />
          </button>
          <button
            v-if="fetchSummary.details?.length"
            @click="showDetails = !showDetails"
            class="text-xs text-brand hover:text-brand flex items-center gap-1"
          >
            {{ showDetails ? '收起明细' : '查看明细' }}
            <ChevronUp v-if="showDetails" class="w-3 h-3" />
            <ChevronDown v-else class="w-3 h-3" />
          </button>
        </div>
      </div>
      <div class="flex flex-wrap gap-2 text-xs" v-if="fetchSummary.success">
        <span class="px-2.5 py-1 rounded-lg bg-subtle text-ink-2">导出 {{ fetchSummary.exported || 0 }} 条</span>
        <span class="px-2.5 py-1 rounded-lg bg-subtle text-ink-2">粗筛 {{ fetchSummary.candidates || 0 }} 条</span>
        <span class="px-2.5 py-1 rounded-lg bg-subtle text-ink-2">过滤闲聊 {{ fetchSummary.dropped || 0 }} 条</span>
        <span class="px-2.5 py-1 rounded-lg bg-success-soft text-success-ink">确认订单 {{ fetchSummary.totalOrders || 0 }} 笔</span>
        <span v-if="fetchSummary.duplicates" class="px-2.5 py-1 rounded-lg bg-subtle text-ink-2">增量去重 {{ fetchSummary.duplicates }} 笔</span>
        <span class="px-2.5 py-1 rounded-lg bg-warning-soft text-warning-ink">待人工审查 {{ fetchSummary.reviewCount || 0 }} 条</span>
        <span v-if="fetchSummary.actionCount" class="px-2.5 py-1 rounded-lg bg-brand-soft border border-brand-border text-brand-ink">订单操作 {{ fetchSummary.actionCount }} 条（已处理 {{ fetchSummary.actionApplied || 0 }}）</span>
        <span v-if="fetchSummary.actionManual" class="px-2.5 py-1 rounded-lg bg-warning-soft text-warning-ink">操作待人工定位 {{ fetchSummary.actionManual }} 条</span>
        <span class="px-2.5 py-1 rounded-lg bg-subtle text-ink-2">{{ fetchSummary.workers || 0 }} 协程</span>
        <span v-if="fetchSummary.failed" class="px-2.5 py-1 rounded-lg bg-danger-soft text-danger-ink">失败 {{ fetchSummary.failed }} 条</span>
      </div>
      <p v-if="fetchSummary.success" class="text-xs text-ink-3 leading-relaxed">
        与目标订单日期一致的确认订单已填入下方「数据预览」；没写日期标记、日期不符或判定存疑的订单停在「待人工审查」区。
        聊天里「这单不要了 / 加五斤 / 改地址」等订单操作会在全部识别完成后单独回放，被处理过的订单带原始→结果对照进入人工审查区，
        由人工决定采用或撤销。确认无误后再点「确认数据无误」写入数据库；本按钮不会自动入库。
      </p>
      <div v-if="showActions && fetchSummary.actions?.length" class="max-h-64 overflow-y-auto custom-scrollbar space-y-1 pt-1">
        <div
          v-for="(a, i) in fetchSummary.actions"
          :key="i"
          class="flex items-start gap-3 px-3 py-2 rounded-lg bg-surface border border-line text-xs"
        >
          <span :class="a.action === 'delete' ? 'text-danger-ink' : 'text-brand-ink'" class="font-bold w-16 shrink-0">{{ a.actionLabel }}</span>
          <span class="text-ink-3 shrink-0 w-32 truncate">{{ a.messageTime }}</span>
          <span class="text-ink-2 shrink-0 w-20 truncate">{{ a.sender }}</span>
          <div class="flex-1 min-w-0">
            <div class="text-ink-2 truncate">「{{ a.rawContent }}」</div>
            <div class="text-ink-3 mt-0.5 truncate">
              {{ a.targetLabel }} · {{ a.matchedBy }} · {{ a.status === 'applied' ? '已处理' : '待人工' }}
              <span v-if="a.changes"> · {{ a.changes }}</span>
            </div>
          </div>
        </div>
      </div>
      <div v-if="showDetails && fetchSummary.details?.length" class="max-h-64 overflow-y-auto custom-scrollbar space-y-1 pt-1">
        <div
          v-for="(d, i) in fetchSummary.details"
          :key="i"
          class="flex items-center gap-3 px-3 py-2 rounded-lg bg-subtle text-xs"
        >
          <span
            :class="d.disposition === '预览' ? 'text-success' : d.disposition === '待审查' ? 'text-warning-ink' : 'text-ink-3'"
            class="font-bold w-14 shrink-0"
          >
            {{ d.disposition || (d.isOrder ? '订单' : (d.error ? '失败' : '非订单')) }}
          </span>
          <span class="text-ink-3 shrink-0 w-32 truncate">{{ d.messageTime }}</span>
          <span class="text-ink-2 shrink-0 w-20 truncate">{{ d.sender }}</span>
          <span class="text-ink-2 truncate flex-1">{{ d.recipient || d.reason || d.error }}</span>
          <span v-if="d.orderDate" class="text-ink-3 shrink-0">{{ d.orderDate }}{{ d.isToday ? ' 今日' : '' }}</span>
        </div>
      </div>
      <div v-if="fetchSummary.warnings?.length" class="text-xs text-warning-ink space-y-0.5">
        <p v-for="(w, i) in fetchSummary.warnings" :key="i">· {{ w }}</p>
      </div>
    </div>

    <div class="relative group">
      <textarea
        :value="inputText"
        @input="$emit('update:inputText', ($event.target as HTMLTextAreaElement).value)"
        placeholder="请在此处输入或粘贴原始订单详情...&#10;示例：王小二 13800138000 季红市夏安区XX路XX号 苹果x1 香蕉x2"
        class="w-full h-48 p-6 rounded-md bg-surface border border-line-strong focus:border-brand outline-none text-ink placeholder:text-ink-3 resize-none font-mono text-sm leading-relaxed transition-colors"
      ></textarea>
      <div class="absolute bottom-4 right-4 flex items-center gap-3">
        <!-- 模式选择：重来 / 追加 分段控件 -->
        <div class="inline-flex bg-subtle rounded-md p-0.5">
          <button
            type="button"
            @click="isAppendMode = false"
            :class="!isAppendMode ? 'bg-surface shadow-sm text-ink rounded' : 'text-ink-2'"
            class="px-3 py-1.5 text-sm rounded font-medium transition-colors"
          >
            重来
          </button>
          <button
            type="button"
            @click="isAppendMode = true"
            :class="isAppendMode ? 'bg-surface shadow-sm text-ink rounded' : 'text-ink-2'"
            class="px-3 py-1.5 text-sm rounded font-medium transition-colors"
          >
            追加
          </button>
        </div>
        <button
          @click="$emit('clear')"
          class="px-4 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover text-sm font-medium transition-colors"
        >
          清空输入
        </button>
        <!-- AI模型选择器 -->
        <div class="ai-model-selector relative">
          <div
            @click="toggleDropdown"
            class="flex items-center gap-2 px-4 py-2 rounded-md border border-line-strong text-ink-2 text-sm font-medium cursor-pointer hover:bg-hover transition-colors"
          >
            <span v-if="isLoadingModels">加载中...</span>
            <span v-else-if="selectedModel">AI模型：{{ selectedModel }}</span>
            <span v-else>选择模型</span>
            <ChevronDown v-if="!isDropdownOpen" class="w-4 h-4" />
            <ChevronUp v-else class="w-4 h-4" />
          </div>

          <!-- 下拉菜单 -->
          <div
            v-if="isDropdownOpen && aiModels.size > 0"
            class="absolute top-full mt-2 w-48 bg-surface border border-line rounded-lg shadow-pop z-10"
          >
            <div
              v-for="model in Array.from(aiModels.keys())"
              :key="model"
              @click="selectModel(model)"
              class="px-4 py-2 text-sm text-ink-2 hover:bg-hover cursor-pointer transition-colors"
              :class="{ 'bg-brand-soft text-brand-ink': model === selectedModel }"
            >
              {{ model }}
            </div>
          </div>

          <!-- 错误提示 -->
          <div v-if="modelError" class="absolute top-full mt-2 w-48 text-xs text-danger">
            {{ modelError }}
          </div>
        </div>
        <button
          @click="$emit('analyze', isAppendMode, aiModels.get(selectedModel) || 0)"
          :disabled="isAnalyzing || !inputText.trim() || !selectedModel"
          class="px-6 py-2 rounded-md bg-brand hover:bg-brand-hover disabled:opacity-50 disabled:cursor-not-allowed text-onbrand text-sm font-medium flex items-center gap-2 shadow-sm transition-colors"
        >
          <template v-if="isAnalyzing">
            <Loader2 class="w-4 h-4 animate-spin" />
            正在识别...
          </template>
          <template v-else>
            识别数据 <Sparkles class="w-4 h-4" />
          </template>
        </button>
      </div>
    </div>
  </div>
</template>
