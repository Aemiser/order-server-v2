<template>
  <div class="bg-surface border border-line rounded-lg p-4">
    <div class="flex flex-col lg:flex-row lg:items-end gap-4">
      <!-- 日期范围选择：默认自动对齐今天 -->
      <div class="flex-1 min-w-0 lg:min-w-[420px] space-y-2">
        <div class="flex items-center justify-between gap-3 flex-wrap">
          <label class="filter-label">
            <Calendar class="w-3.5 h-3.5 text-brand/70" />
            日期范围
            <span class="normal-case tracking-normal font-medium text-ink-3 text-[11px]">默认对齐今天</span>
          </label>

          <!-- 快捷区间 -->
          <div class="flex items-center gap-1.5">
            <button
              v-for="quick in quickRanges"
              :key="quick.key"
              type="button"
              @click="applyQuickRange(quick.key)"
              class="px-2.5 py-1 rounded-md border text-[11px] font-medium transition-colors"
              :class="activeQuick === quick.key
                ? 'bg-brand-soft border-brand-border text-brand'
                : 'bg-surface border-line text-ink-3 hover:text-brand hover:border-brand-border'"
            >
              {{ quick.label }}
            </button>
          </div>
        </div>

        <div class="relative date-range-field">
          <ElDatePicker
            v-model="dateRange"
            type="daterange"
            value-format="YYYY-MM-DD"
            range-separator="至"
            start-placeholder="开始日期"
            end-placeholder="结束日期"
            class="w-full"
            :popper-class="'date-picker-popper'"
            :clearable="false"
            size="large"
            @change="handleDateChange"
          />
        </div>
      </div>

      <!-- 查询 + 审核 -->
      <div class="flex w-full lg:w-auto shrink-0 gap-2">
        <button
          @click="handleQuery"
          class="flex-1 lg:flex-none h-[46px] px-6 rounded-md bg-brand hover:bg-brand-hover text-onbrand font-medium shadow-pop transition-colors flex items-center justify-center gap-2 active:scale-95 text-sm"
        >
          <Search class="w-4 h-4" />
          <span>立即查询</span>
        </button>
        <button
          @click="handleAudit"
          :disabled="!hasQueried || auditBusy"
          :title="!hasQueried ? '请先点「立即查询」拿到账单订单，再按当前日期范围审核' : '导出所选日期范围的群聊、识别订单并逐笔核对账单'"
          class="flex-1 lg:flex-none h-[46px] px-5 rounded-md border border-brand-border bg-brand-soft hover:opacity-90 text-brand-ink font-medium transition-all flex items-center justify-center gap-2 text-sm disabled:opacity-40 disabled:cursor-not-allowed"
        >
          <Loader2 v-if="auditBusy" class="w-4 h-4 animate-spin" />
          <ShieldCheck v-else class="w-4 h-4" />
          <span>{{ auditBusy ? '审核中…' : '审核' }}</span>
        </button>
      </div>
    </div>

    <!-- 审核前置条件 / 进度：让「为什么不能点」一眼可见 -->
    <div v-if="!hasQueried && !auditBusy" class="mt-3 flex items-center gap-1.5 text-[11px] text-ink-3">
      <Info class="w-3.5 h-3.5 shrink-0" />
      先「立即查询」获取账单订单，才能按当前日期范围回查群聊做审核。
    </div>
    <div v-if="auditBusy && auditProgress" class="mt-3 flex items-center gap-2 px-3 py-2 rounded-md bg-brand-soft border border-brand-border text-xs text-brand-ink">
      <Loader2 class="w-3.5 h-3.5 animate-spin shrink-0" />
      <span class="truncate">{{ auditProgress }}</span>
    </div>

    <!-- 错误提示 -->
    <div v-if="errors.dateRange" class="mt-3">
      <p class="text-danger text-xs font-medium">{{ errors.dateRange }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, nextTick } from 'vue';
import { Search, Calendar, ShieldCheck, Loader2, Info } from 'lucide-vue-next';
import { ElDatePicker } from 'element-plus';

// 审核按钮由父组件统一控制忙碌状态与进度文案，避免两个子组件各跑一份审核。
const props = defineProps<{
  auditBusy?: boolean;
  auditProgress?: string;
}>();

type QuickRangeKey = 'today' | 'week' | 'month';

const quickRanges: { key: QuickRangeKey; label: string }[] = [
  { key: 'today', label: '今天' },
  { key: 'week', label: '近 7 天' },
  { key: 'month', label: '本月' }
];

// 按本地时区格式化，避免 toISOString 的 UTC 偏移让日期差一天
const formatDate = (date: Date): string => {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, '0');
  const d = String(date.getDate()).padStart(2, '0');
  return `${y}-${m}-${d}`;
};

// 所有快捷区间都以今天为终点
const buildRange = (key: QuickRangeKey): [string, string] => {
  const today = new Date();
  today.setHours(0, 0, 0, 0);
  const start = new Date(today);
  if (key === 'week') start.setDate(start.getDate() - 6);
  if (key === 'month') start.setDate(1);
  return [formatDate(start), formatDate(today)];
};

// 定义组件事件
const emit = defineEmits<{
  (e: 'query', params: {
    dateStart: string;
    dateEnd: string;
    category: string;
    keyword: string;
    data?: any;
  }): void;
  (e: 'generate-test-data', testData: CheckoutResponse): void;
  (e: 'show-field-dialog', data: {
    fieldList: string[];
    pbMap: Record<string, string[]>;
  }): void;
  (e: 'update-field-data', data: {
    fieldList: string[];
    pbMap: Record<string, string[]>;
  }): void;
  (e: 'field-data-loading', isLoading: boolean): void;
  (e: 'audit', params: { dateStart: string; dateEnd: string }): void;
}>();

// 状态管理：进入页面即自动对齐今天
const dateRange = ref<[string, string] | null>(buildRange('today'));
const activeQuick = ref<QuickRangeKey | null>('today');
// 只有「立即查询」成功返回后才解锁审核按钮；换了日期必须重新查询，
// 否则审核会用新日期的群聊去核对旧日期的账单，结果没有意义。
const hasQueried = ref(false);
const selectedCategory = ref('所有品种');
const searchKeyword = ref('');

// 错误信息
const errors = ref({
  dateRange: ''
});

// 快捷区间：重置为以今天为终点的范围
const applyQuickRange = (key: QuickRangeKey) => {
  dateRange.value = buildRange(key);
  errors.value.dateRange = '';
  hasQueried.value = false;
  // 程序化赋值若触发 change，nextTick 后再补回高亮状态
  nextTick(() => { activeQuick.value = key; });
};

// 每次进入账单结算页重新对齐今天，跨天常驻也不会停留在旧日期
onMounted(() => {
  dateRange.value = buildRange('today');
  activeQuick.value = 'today';
  hasQueried.value = false;
});

// 处理日期变化
const handleDateChange = () => {
  // 清除错误信息
  errors.value.dateRange = '';
  hasQueried.value = false;
  // 用户手动选择后不再高亮快捷区间
  activeQuick.value = null;
};

// 处理查询
const handleQuery = async () => {
  try {
    // 验证日期输入
    if (!dateRange.value || dateRange.value.length !== 2) {
      errors.value.dateRange = '请选择日期范围';
      return;
    }
    
    // 验证日期格式
    if (!dateRange.value[0] || !dateRange.value[1]) {
      errors.value.dateRange = '请选择有效的日期范围';
      return;
    }
    
    // ElDatePicker 配了 value-format="YYYY-MM-DD"，这里拿到的必定是字符串。
    // 按本地时区拼接，不用 toISOString，避免 UTC 偏移让结束日期被推到次日。
    const startDate = new Date(dateRange.value[0] + 'T00:00:00');
    const endDate = new Date(dateRange.value[1] + 'T23:59:59');
    
    // 验证日期对象是否有效
    if (isNaN(startDate.getTime()) || isNaN(endDate.getTime())) {
      errors.value.dateRange = '请选择有效的日期范围';
      return;
    }
    
    if (endDate < startDate) {
      errors.value.dateRange = '截至时间不能小于起始时间，请重新选择时间范围';
      return;
    }
    
    // 清除错误信息
    errors.value.dateRange = '';
    
    // 通知父组件数据正在加载
    emit('field-data-loading', true);
    
    // 日志输出：前端选择的日期范围
    console.log('前端选择的日期范围:', {
      start: dateRange.value[0],
      end: dateRange.value[1],
      startDate: startDate,
      endDate: endDate,
      startDateUTC: startDate.toUTCString(),
      endDateUTC: endDate.toUTCString()
    });
    
    // 调用后端 GetChackDaydata 函数
    const response = await window['go']['main']['App']['GetChackDaydata'](
      startDate,
      endDate
    );
    
    console.log('后端响应:', response);
    
    // 确保响应数据结构完整
    const validResponse = {
      FieldList: response.FieldList || [],
      Count: response.Count || 0,
      ProSPecs: response.ProSPecs || [],
      TableData: response.TableData || [],
      PBMap: response.PBMap || {},
      TotalRow: response.TotalRow || [],
      PricePerKgRow: response.PricePerKgRow || [],
      PricePerItemRow: response.PricePerItemRow || [],
      totalAmountRow: response.TotalAmountRow || response.totalAmountRow || [],
    };
    
    // 无论 FieldList 是否有值，都传递字段数据给父组件
    emit('update-field-data', {
      fieldList: validResponse.FieldList,
      pbMap: validResponse.PBMap
    });

    // 查询成功后解锁审核按钮
    hasQueried.value = true;

    // 表格数据无条件刷新：原来只有「没有新商品」时才 emit query，
    // 第一次查询命中新商品时会让用户误以为点了没反应。
    emit('query', {
      dateStart: dateRange.value[0],
      dateEnd: dateRange.value[1],
      category: selectedCategory.value,
      keyword: searchKeyword.value,
      data: validResponse
    });

    // 有新商品时另外弹出字段配置弹窗，与表格刷新互不阻塞
    if (validResponse.FieldList && validResponse.FieldList.length > 0) {
      emit('show-field-dialog', {
        fieldList: validResponse.FieldList,
        pbMap: validResponse.PBMap
      });
    }
  } catch (error) {
    console.error('查询失败:', error);
    errors.value.dateRange = '查询失败，请稍后重试';
    hasQueried.value = false;
  } finally {
    // 通知父组件数据加载已完成
    emit('field-data-loading', false);
  }
};

// 审核：把当前日期范围交给父组件，由它统一调用后端并刷新人工审核区。
const handleAudit = () => {
  if (props.auditBusy) return;
  if (!hasQueried.value) {
    errors.value.dateRange = '请先点「立即查询」获取账单订单，再执行审核';
    return;
  }
  if (!dateRange.value || dateRange.value.length !== 2 || !dateRange.value[0] || !dateRange.value[1]) {
    errors.value.dateRange = '请选择有效的日期范围';
    return;
  }
  if (dateRange.value[1] < dateRange.value[0]) {
    errors.value.dateRange = '截至日期不能小于起始日期';
    return;
  }
  errors.value.dateRange = '';
  emit('audit', { dateStart: dateRange.value[0], dateEnd: dateRange.value[1] });
};

// 后端数据结构
interface ProSPecs {
  SID: number;
  Name: string;
  Specs: string[];
}

interface CheckoutResponse {
  FieldList: string[];
  Count: number;
  ProSPecs: ProSPecs[];
  TableData: string[];
  PBMap: Record<string, string[]>;
}

// 生成模拟数据
const generateMockCheckoutData = (): CheckoutResponse => {
  // 商品数据
  const products = [
    { name: '香水柠檬', specs: ['5斤', '8斤', '10斤', '20斤'] },
    { name: '广香一级', specs: ['10斤', '20斤'] },
    { name: '广香二级', specs: ['7斤', '15斤'] },
    { name: '黄柠檬', specs: ['3斤', '6斤'] },
    { name: '金桔', specs: ['2斤', '4斤'] }
  ];
  
  // 转换为 ProSPecs 格式
  const proSPecs = products.map((product, index) => ({
    SID: index + 1,
    Name: product.name,
    Specs: product.specs
  }));
  
  // 生成表格数据
  const tableData = [];
  for (let i = 8; i <= 18; i++) {
    const date = `3月${i}日`;
    const values = [];
    // 为每个规格生成随机数量
    products.forEach(product => {
      product.specs.forEach(() => {
        const quantity = Math.floor(Math.random() * 30);
        values.push(quantity > 0 ? quantity.toString() : '—');
      });
    });
    tableData.push(`${date},${values.join(',')}`);
  }
  
  return {
    FieldList: [],
    Count: tableData.length,
    ProSPecs: proSPecs,
    TableData: tableData,
    PBMap: {
      '客户信息': ['客户姓名', '联系方式'],
      '配送信息': ['配送地址'],
      '订单要求': ['商品信息'],
      '备注说明': ['特殊要求'],
      '操作选项': ['打印选项']
    }
  };
};

// 生成测试数据
const handleGenerateTestData = () => {
  const mockData = generateMockCheckoutData();
  console.log('生成的测试数据:', mockData);
  emit('generate-test-data', mockData);
  console.log('测试数据生成请求已发送');
};
</script>

<style scoped>
.filter-label {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-height: 14px;
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0.1em;
  text-transform: uppercase;
  color: var(--color-ink-3);
}

/* 日期区间控件：更长更高，日期文本更醒目 */
.date-range-field :deep(.el-date-editor) {
  width: 100%;
  min-width: 420px;
  height: 46px;
  box-sizing: border-box;
  padding: 0 16px;
  border-radius: 8px;
  background-color: var(--color-surface) !important;
  box-shadow: 0 0 0 1px var(--color-line-strong) inset !important;
  transition: box-shadow 150ms ease;
}
.date-range-field :deep(.el-date-editor:hover) {
  box-shadow: 0 0 0 1px #b9b9b2 inset !important;
}
.date-range-field :deep(.el-date-editor.is-active),
.date-range-field :deep(.el-input__wrapper.is-focus) {
  box-shadow: 0 0 0 1px var(--color-brand) inset,
    0 0 0 3px rgba(45, 100, 184, 0.12) !important;
}
.date-range-field :deep(.el-range-input) {
  font-size: 14px;
  font-weight: 600;
  color: var(--color-ink);
}
.date-range-field :deep(.el-range-input::placeholder) {
  color: var(--color-ink-3);
  font-weight: 400;
}
.date-range-field :deep(.el-range__icon) {
  color: var(--color-brand);
  opacity: 0.8;
  font-size: 16px;
}
.date-range-field :deep(.el-range-separator) {
  color: var(--color-ink-3);
  font-size: 13px;
  padding: 0 10px;
}

/* 窄屏不强制最小宽度，避免横向溢出 */
@media (max-width: 640px) {
  .date-range-field :deep(.el-date-editor) {
    min-width: 0;
  }
}
</style>