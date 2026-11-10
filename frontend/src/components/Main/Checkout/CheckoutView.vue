<template>
  <div class="space-y-6">
    <div>
      <h2 class="text-ink font-semibold text-xl">账单结算</h2>
      <p class="text-ink-3 text-sm mt-1">按日期范围查询并结算账单数据</p>
    </div>

    <!-- Query Area -->
    <CheckQuery
      :auditBusy="auditBusy"
      :auditProgress="auditProgress"
      @query="handleQuery"
      @audit="handleAudit"
      @generate-test-data="handleGenerateTestData"
      @show-field-dialog="handleShowFieldDialog"
      @update-field-data="handleUpdateFieldData"
      @field-data-loading="handleFieldDataLoading"
    />

    <!-- Table Area -->
    <CheckTable 
      :testData="testData" 
      :checkoutResponse="props.checkoutResponse"
      :isEditMode="props.isEditMode" 
      @save-prices="handleSavePrices"
      @save-unit="handleSaveUnit"
      @enter-edit-mode="handleEnterEditMode"
      @exit-edit-mode="handleExitEditMode"
      @clear-prices="handleClearPrices"
      @data-status="handleDataStatus"
    />

    <!-- Audit Review Area：只有跑过一轮审核才展开，避免空区块干扰结算表格 -->
    <CheckoutReview
      v-if="auditHasRun"
      :items="auditItems"
      :summary="auditSummary"
      :busy="auditBusy"
      :progress="auditProgress"
      :error="auditError"
      @promote="handleAuditPromote"
      @discard="handleAuditDiscard"
    />

    <!-- Field Configuration Dialog -->
    <FieldDialog 
      v-if="showFieldDialog" 
      :show="showFieldDialog"
      :fieldList="fieldList"
      :pbMap="pbMap"
      @close="handleCloseFieldDialog"
      @save="handleSaveFieldConfig"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { EventsOn, EventsOff } from '../../../../wailsjs/runtime/runtime';
import CheckQuery from './CheckQuery.vue';
import CheckTable from './CheckTable.vue';
import CheckoutReview from './CheckoutReview.vue';
import FieldDialog from '../Dialog/Field.vue';

// 日期范围由 CheckQuery 自动对齐今天，不再传固定初始值

// 字段配置弹窗显示状态
const showFieldDialog = ref(false);

// 测试数据状态
const testData = ref<any>(null);

// 字段列表和商品归属map
const fieldList = ref<string[]>([]);
const pbMap = ref<Record<string, string[]>>({});

// 存储从CheckQuery组件获取的字段数据
const fieldData = ref<{
  fieldList: string[];
  pbMap: Record<string, string[]>
}>({
  fieldList: [],
  pbMap: {}
});

// 编辑模式状态
const props = defineProps<{
  isEditMode?: boolean;
  checkoutResponse?: any;
}>();

// 定义事件
const emit = defineEmits<{
  (e: 'save-prices', prices: (number | string)[]): void;
  (e: 'save-unit', unit: string): void;
  (e: 'enter-edit-mode'): void;
  (e: 'exit-edit-mode'): void;
  (e: 'clear-prices'): void;
  (e: 'data-status', hasData: boolean): void;
  (e: 'date-range-change', dateRange: {start: string, end: string}): void;
  (e: 'update-field-data', data: {
    fieldList: string[];
    pbMap: Record<string, string[]>;
  }): void;
  (e: 'field-data-loading', isLoading: boolean): void;
  // 审核区「加入预览区」会写后端 global.AddressList，必须同步刷新 App.vue 的预览表
  (e: 'preview-changed', rows: any[]): void;
}>();

// ---------------- 账单审核 ----------------
const auditBusy = ref(false);
const auditProgress = ref('');
const auditError = ref('');
const auditItems = ref<any[]>([]);
const auditSummary = ref<any>(null);
const auditHasRun = ref(false);

const app = () => (window as any)['go']['main']['App'];

const refreshAuditReviews = async () => {
  const [list, summary] = await Promise.all([
    app()['GetCheckoutAuditReviews'](),
    app()['GetCheckoutAuditSummary']()
  ]);
  auditItems.value = Array.isArray(list) ? list : [];
  if (summary && typeof summary === 'object') auditSummary.value = summary;
};

// 审核结果存在 Go 侧，切到别的页再回来也能看到上一轮结果。
onMounted(async () => {
  try {
    await refreshAuditReviews();
    if (auditItems.value.length || auditSummary.value?.from) auditHasRun.value = true;
  } catch (e) {
    console.warn('加载历史审核结果失败:', e);
  }
  EventsOn('checkout-audit-progress', (p: any) => {
    if (p && p.message) auditProgress.value = p.message;
  });
});
onUnmounted(() => { EventsOff('checkout-audit-progress'); });

// 按当前日期范围导出群聊 → 复用一键识别的识别逻辑 → 与账单订单逐笔核对。
const handleAudit = async (params: { dateStart: string; dateEnd: string }) => {
  if (auditBusy.value) return;
  auditBusy.value = true;
  auditHasRun.value = true;
  auditError.value = '';
  auditProgress.value = '准备导出群聊记录…';
  emit('date-range-change', { start: params.dateStart, end: params.dateEnd });
  try {
    const result = await app()['AuditCheckoutOrders'](params.dateStart, params.dateEnd);
    auditSummary.value = result || null;
    if (!result?.success) auditError.value = result?.message || '审核未完成';
    await refreshAuditReviews();
    if (!auditError.value && auditSummary.value) {
      auditSummary.value = { ...auditSummary.value, message: result?.message || auditSummary.value.message };
    }
  } catch (e: any) {
    auditError.value = '审核失败：' + (e?.message || String(e));
  } finally {
    auditBusy.value = false;
    auditProgress.value = '';
  }
};

// 人工确认的漏单追加到「数据预览区」（仍不入库），并同步 App.vue 的预览表。
const handleAuditPromote = async (ids: string[]) => {
  if (auditBusy.value) return;
  auditBusy.value = true;
  try {
    const latest = await app()['PromoteCheckoutAuditReviews'](ids);
    if (Array.isArray(latest)) emit('preview-changed', latest);
    const left = await app()['GetCheckoutAuditReviews']();
    auditItems.value = Array.isArray(left) ? left : [];
    auditSummary.value = {
      ...(auditSummary.value || {}),
      message: '已把确认的漏单加入「订单输入」页的数据预览区；请在那里确认入库后，再回来重新查询账单。'
    };
  } catch (e: any) {
    auditError.value = '加入预览区失败：' + (e?.message || String(e));
  } finally {
    auditBusy.value = false;
  }
};

const handleAuditDiscard = async (ids: string[]) => {
  if (auditBusy.value) return;
  auditBusy.value = true;
  try {
    const left = await app()['DiscardCheckoutAuditReviews'](ids);
    auditItems.value = Array.isArray(left) ? left : [];
    auditSummary.value = {
      ...(auditSummary.value || {}),
      message: '已忽略所选条目；忽略不会改动数据预览区，也不会写数据库。'
    };
  } catch (e: any) {
    auditError.value = '忽略失败：' + (e?.message || String(e));
  } finally {
    auditBusy.value = false;
  }
};

// 处理保存价格
const handleSavePrices = (proPriceRequests: any[]) => {
  emit('save-prices', proPriceRequests);
};

// 处理保存计量单位
const handleSaveUnit = (unit: string) => {
  emit('save-unit', unit);
};

// 处理进入编辑模式
const handleEnterEditMode = () => {
  emit('enter-edit-mode');
};

// 处理退出编辑模式
const handleExitEditMode = () => {
  emit('exit-edit-mode');
};

// 处理清空价格
const handleClearPrices = () => {
  emit('clear-prices');
};

// 处理数据状态变化
const handleDataStatus = (hasData: boolean) => {
  emit('data-status', hasData);
};

// 处理查询
const handleQuery = (params: {
  dateStart: string;
  dateEnd: string;
  category: string;
  keyword: string;
  data?: any;
}) => {
  console.log('查询参数:', params);
  
  // 传递日期范围给父组件
  emit('date-range-change', {
    start: params.dateStart,
    end: params.dateEnd
  });
  
  // 确保数据结构完整
  if (params.data) {
    // 验证数据结构
    const validData = {
      FieldList: params.data.FieldList || [],
      Count: params.data.Count || 0,
      ProSPecs: params.data.ProSPecs || [],
      TableData: params.data.TableData || [],
      PBMap: params.data.PBMap || {},
      TotalRow: params.data.TotalRow || [],
      PricePerKgRow: params.data.PricePerKgRow || [],
      PricePerItemRow: params.data.PricePerItemRow || [],
      totalAmountRow: params.data.TotalAmountRow || params.data.totalAmountRow || [],
    };
    
    // 更新测试数据，触发 CheckTable 组件的更新
    testData.value = validData;
    console.log('数据已传递给 CheckTable 组件');
  }
};

// 处理测试数据生成
const handleGenerateTestData = (data: any) => {
  console.log('收到测试数据:', data);
  testData.value = data;
};

// 处理显示字段配置弹窗
const handleShowFieldDialog = (data: {
  fieldList: string[];
  pbMap: Record<string, string[]>;
}) => {
  console.log('显示字段配置弹窗:', data);
  fieldList.value = data.fieldList;
  
  pbMap.value = data.pbMap;
  
  showFieldDialog.value = true;
};

// 处理关闭字段配置弹窗
const handleCloseFieldDialog = () => {
  showFieldDialog.value = false;
};

// 处理保存字段配置
const handleSaveFieldConfig = (fieldMap: Record<string, string[]>) => {
  console.log('保存字段配置:', fieldMap);
  
  try {
    // 调用后端 SetProductBelong 函数
    window['go']['main']['App']['SetProductBelong'](fieldMap);
    console.log('字段配置保存成功');
  } catch (error) {
    console.error('保存字段配置失败:', error);
    alert('保存字段配置失败，请稍后重试');
  }
  
  showFieldDialog.value = false;
};

// 处理刷新
const handleRefresh = () => {
  console.log('刷新数据');
  // 这里可以添加实际的刷新逻辑
};

// 处理字段数据更新
const handleUpdateFieldData = (data: {
  fieldList: string[];
  pbMap: Record<string, string[]>;
}) => {
  // 存储字段数据
  fieldData.value = data;
  // 传递给父组件
  emit('update-field-data', data);
  console.log('字段数据已更新:', data);
};

// 处理字段数据加载状态
const handleFieldDataLoading = (isLoading: boolean) => {
  // 传递给父组件
  emit('field-data-loading', isLoading);
  console.log('字段数据加载状态:', isLoading);
};

// 处理字段配置
const handleConfig = () => {
  // 使用存储的字段数据显示弹窗
  fieldList.value = fieldData.value.fieldList;
  pbMap.value = fieldData.value.pbMap;
  showFieldDialog.value = true;
};

// 处理结算
const handleCheckout = () => {
  alert('正在生成财务账单结算数据...');
  // 这里可以添加实际的结算逻辑
};
</script>

<style scoped>
/* 滚动条已由全局 .custom-scrollbar 处理 */
</style>