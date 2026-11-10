<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import {
  Download,
  ChevronDown,
  ChevronRight,
  User,
  Phone,
  MapPin,
  Calendar,
  RotateCcw,
  Search,
  ChevronLeft
} from 'lucide-vue-next';
import { OrderQuery } from '../../../wailsjs/go/main/App';
import { ElDatePicker } from 'element-plus';
import * as XLSX from "xlsx";

const props = defineProps<{
  activeTab?: string;
}>();

// 监听 activeTab 变化，当切换到详情页时调用 resetQuery
watch(() => props.activeTab, (newTab) => {
  if (newTab === 'details') {
    resetQuery();
  }
});

// 组件挂载时调用 resetQuery
onMounted(() => {
  resetQuery();
});

const isExpanded = ref(true);

const toggleQuery = () => {
  isExpanded.value = !isExpanded.value;
};

// 分页相关状态
const pageSize = ref(10); // 每页显示条数
const currentPage = ref(1); // 当前页码
const totalPages = ref(1); // 总页数
const isLoading = ref(false); // 加载状态

// 每页显示条数选项
const pageSizeOptions = [5, 10, 20,50,100];

// 查询参数
const searchParams = ref({
  name: '',
  phone: '',
  address: '',
  item: '',
  keyword: '',
  startTime: '',
  endTime: '',
});

// 日期范围（Element Plus daterange 默认返回 Date 数组）
const dateRange = ref<Date[] | null>(null);
const activeShortcut = ref('');

// 错误信息
const errors = ref({
  dateRange: ''
});

// 本地日期工具：统一 YYYY-MM-DD，避免 toISOString() 造成的时区偏移
const formatLocalDate = (date: Date) =>
  `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;

const parseLocalDate = (value: string, endOfDay = false) => {
  const [year, month, day] = value.split('-').map(Number);
  return endOfDay
    ? new Date(year, month - 1, day, 23, 59, 59, 999)
    : new Date(year, month - 1, day, 0, 0, 0, 0);
};

// 处理日期变化（用户手动选择 / 清空时触发）
const handleDateChange = () => {
  errors.value.dateRange = '';
  activeShortcut.value = '';

  if (dateRange.value && dateRange.value.length === 2) {
    searchParams.value.startTime = formatLocalDate(dateRange.value[0]);
    searchParams.value.endTime = formatLocalDate(dateRange.value[1]);
  } else {
    searchParams.value.startTime = '';
    searchParams.value.endTime = '';
  }
};

// 常用时间快捷项
const dateShortcuts = [
  {
    key: 'today', label: '今天',
    range: () => { const now = new Date(); return [new Date(now), new Date(now)] as [Date, Date]; }
  },
  {
    key: 'yesterday', label: '昨天',
    range: () => { const d = new Date(); d.setDate(d.getDate() - 1); return [new Date(d), new Date(d)] as [Date, Date]; }
  },
  {
    key: 'week', label: '近 7 天',
    range: () => { const end = new Date(); const start = new Date(); start.setDate(start.getDate() - 6); return [start, end] as [Date, Date]; }
  },
  {
    key: 'month', label: '近 30 天',
    range: () => { const end = new Date(); const start = new Date(); start.setDate(start.getDate() - 29); return [start, end] as [Date, Date]; }
  },
  {
    key: 'thisMonth', label: '本月',
    range: () => { const end = new Date(); return [new Date(end.getFullYear(), end.getMonth(), 1), end] as [Date, Date]; }
  }
];

// 应用快捷时间：同步日期控件与查询参数，并立即查询
const applyDateShortcut = (shortcut: { key: string; range: () => [Date, Date] }) => {
  if (isLoading.value) return;
  const [start, end] = shortcut.range();
  dateRange.value = [start, end];
  searchParams.value.startTime = formatLocalDate(start);
  searchParams.value.endTime = formatLocalDate(end);
  activeShortcut.value = shortcut.key;
  errors.value.dateRange = '';
  currentPage.value = 1;
  fetchOrderData();
};

// 清空时间条件
const clearDateRange = () => {
  dateRange.value = null;
  searchParams.value.startTime = '';
  searchParams.value.endTime = '';
  activeShortcut.value = '';
  errors.value.dateRange = '';
  currentPage.value = 1;
  fetchOrderData();
};

// 验证日期范围（YYYY-MM-DD 可直接按字典序比较）
const validateDateRange = () => {
  const { startTime, endTime } = searchParams.value;
  if (startTime && endTime && endTime < startTime) {
    errors.value.dateRange = '截至时间不能早于起始时间';
    return false;
  }
  errors.value.dateRange = '';
  return true;
};

// 移除日期变化的自动监听，只在点击查询按钮时进行验证

// Mock data for the table
const orderDetails = ref([
  { name: '张晓明', phone: '139-1234-5678', address: '浙江省杭州市西湖区文三路...', item: '【香水柠檬5斤】', quantity: 1, time: '2023-11-20 14:32:11', remark: '-' },
  { name: '李华', phone: '186-9988-7766', address: '华夏省大兵市南山区高新园...', item: '【香水柠檬10斤】,【广香一级20斤】', quantity: 2, time: '2023-11-20 15:05:44', remark: '-' },
  { name: '王力群', phone: '155-0011-2233', address: '上海市浦东新区张江高科...', item: '【黄柠檬3斤】', quantity: 1, time: '2023-11-21 09:12:05', remark: '-' },
  { name: '陈思琪', phone: '177-4455-6677', address: '北京市朝阳区三里屯...', item: '【金桔2斤】', quantity: 1, time: '2023-11-21 11:45:30', remark: '-' },
  { name: '赵雷', phone: '131-0099-8877', address: '四川省成都市武侯区...', item: '【青柠尤力克8斤】', quantity: 3, time: '2023-11-21 16:20:12', remark: '-' },
]);

// 总订单数
const totalOrders = ref(12);

// 防抖函数
const debounce = (func: Function, delay: number) => {
  let timeoutId: ReturnType<typeof setTimeout>;
  return (...args: any[]) => {
    clearTimeout(timeoutId);
    timeoutId = setTimeout(() => func.apply(null, args), delay);
  };
};

// 关键词搜索防抖
const handleKeywordSearch = debounce(() => {
  fetchOrderData();
}, 500);

// 计算总页数
const calculateTotalPages = () => {
  totalPages.value = Math.ceil(totalOrders.value / pageSize.value);
  // 确保当前页码不超过总页数
  if (currentPage.value > totalPages.value) {
    currentPage.value = Math.max(1, totalPages.value);
  }
};

// 计算展示的页码列表
const displayPages = computed(() => {
  const total = totalPages.value;
  const current = currentPage.value;
  const pages: ({ type: 'page'; value: number } | { type: 'ellipsis'; value: number })[] = [];

  if (total <= 5) {
    for (let i = 1; i <= total; i++) {
      pages.push({ type: 'page', value: i });
    }
    return pages;
  }

  pages.push({ type: 'page', value: 1 });

  const start = Math.max(2, current - 3);
  const end = Math.min(total - 1, current + 3);

  if (start > 2) {
    pages.push({ type: 'ellipsis', value: -1 });
  }

  for (let i = start; i <= end; i++) {
    pages.push({ type: 'page', value: i });
  }

  if (end < total - 1) {
    pages.push({ type: 'ellipsis', value: -1 });
  }

  pages.push({ type: 'page', value: total });

  return pages;
});

// 切换页码
const changePage = debounce((page: number) => {
  if (page < 1 || page > totalPages.value) return;
  console.log('页码总数:', totalPages.value);
  console.log('切换页码:', page);
  currentPage.value = page;
  fetchOrderData();
}, 300);

// 改变每页显示条数
const changePageSize = debounce((size: number) => {
  pageSize.value = size;
  currentPage.value = 1; // 重置到第一页
  calculateTotalPages();
  fetchOrderData();
}, 300);

// 上一页
const prevPage = () => {
  if (currentPage.value > 1) {
    changePage(currentPage.value - 1);
  }
};

// 下一页
const nextPage = () => {
  if (currentPage.value < totalPages.value) {
    changePage(currentPage.value + 1);
  }
}; 

// 获取订单数据
const fetchOrderData = async () => {
  isLoading.value = true;
  try {
    // 验证日期范围
    if (!validateDateRange()) {
      return;
    }
    
    // 构建 addresssinfo 结构体
    const addresssinfo = {
      "收件人": searchParams.value.name || '',
      "手机号": searchParams.value.phone || '',
      "收货地址": searchParams.value.address || '',
      "备注": ''
    };
    
    // 创建完整的请求参数，包含 addresssinfo 字段
    const requestParams: any = {
      limit: pageSize.value,
      page: currentPage.value,
      key: searchParams.value.keyword || '',
      order: '',
      addresssinfo: addresssinfo
    };
    
    // 只有当日期不为空时才添加到请求参数中（按本地时区的起止时刻）
    if (searchParams.value.startTime) {
      requestParams.Starttime = parseLocalDate(searchParams.value.startTime).toISOString();
    }
    if (searchParams.value.endTime) {
      requestParams.Endtime = parseLocalDate(searchParams.value.endTime, true).toISOString();
    }
    
    console.log('请求参数:', requestParams);
    
    // 调用后端的 OrderQuery 函数
    const result = await OrderQuery(requestParams);
    console.log('查询结果:', result);
    
    // 检查返回值类型
    if (result && typeof result === 'object') {
      // 检查是否包含 list 和 count 字段
      if (Array.isArray(result.list) && (typeof result.count === 'number' || typeof result.count === 'string')) {

        // 更新总订单数
        totalOrders.value = Number(result.count);
        calculateTotalPages();
        
        // 根据返回的结果更新订单列表
        orderDetails.value = result.list.map((item: any) => ({
          name: item['收件人'] || '未知姓名',
          phone: item['手机号'] || '未知电话',
          address: item['收货地址'] || '未知地址',
          item: item['商品规格'] || '-',
          quantity: 1, // 后端返回的数据中可能没有数量字段，默认设为 1
          time: item.createdAt ? new Date(item.createdAt).toLocaleString() : '未知时间',
          remark: item['备注'] || '-'
        }));
      } else if (result.list == null){
        // 说明没找到数据
        orderDetails.value = [];
        totalOrders.value = Number(result.count);
        calculateTotalPages();
      }else{
        // 如果格式不正确，将订单列表设为空，总订单数保持不变
        orderDetails.value = [];
        // 不更新总订单数，保持之前的值
        calculateTotalPages();
      }
    } else {
      // 如果返回的是其他类型，将订单列表设为空，总订单数保持不变
      orderDetails.value = [];
      // 不更新总订单数，保持之前的值
      calculateTotalPages();
    }
  } catch (error) {
    console.error('查询订单失败:', error);
    // 发生错误时，将订单列表设为空，总订单数保持不变
    orderDetails.value = [];
    // 不更新总订单数，保持之前的值
    calculateTotalPages();
  } finally {
    isLoading.value = false;
  }
};

const exportToExcel = async () => {
  isLoading.value = true;
  try {
    const addresssinfo = {
      "收件人": searchParams.value.name || '',
      "手机号": searchParams.value.phone || '',
      "收货地址": searchParams.value.address || '',
      "备注": ''
    };

    const requestParams: any = {
      limit: 0,   // limit=0 获取全部数据
      page: 1,
      key: searchParams.value.keyword || '',
      order: '',
      addresssinfo: addresssinfo
    };
    if (searchParams.value.startTime) {
      requestParams.Starttime = parseLocalDate(searchParams.value.startTime).toISOString();
    }
    if (searchParams.value.endTime) {
      requestParams.Endtime = parseLocalDate(searchParams.value.endTime, true).toISOString();
    }

    const result = await OrderQuery(requestParams);

    if (result && Array.isArray(result.list)) {
      const exportData = result.list.map((item: any) => ({
        '日期': item.createdAt ? new Date(item.createdAt).toLocaleString() : '未知时间',
        '收件人': item['收件人'] || '',
        '手机号': item['手机号'] || '',
        '收货地址': item['收货地址'] || '',
        '商品规格': item['商品规格'] || '',
        '备注': item['备注'] || ''
      }));

      const worksheet = XLSX.utils.json_to_sheet(exportData);
      const workbook = XLSX.utils.book_new();
      XLSX.utils.book_append_sheet(workbook, worksheet, "Orders");
      const startLabel = searchParams.value.startTime;
      const endLabel = searchParams.value.endTime;
      const rangeLabel = startLabel && endLabel
        ? `${startLabel}-${endLabel}`
        : (startLabel || endLabel || formatLocalDate(new Date()));
      XLSX.writeFile(workbook, `Orders_${rangeLabel}.xlsx`);
    }
  } catch (error) {
    console.error('导出订单失败:', error);
  } finally {
    isLoading.value = false;
  }
};


/* ---------------- 规格标签：按商品规格区分颜色 ---------------- */
// 浅色主题的柔和色板（浅底 + 深字 + 细描边），与全局设计令牌保持同一调性
const specPalette = [
  { bg: '#edf3fb', ink: '#24539b', border: '#c7daf2' }, // 品牌蓝
  { bg: '#e5f4f2', ink: '#1f6f68', border: '#bfe0db' }, // 青
  { bg: '#e8f2ed', ink: '#246048', border: '#c6e0d2' }, // 绿
  { bg: '#f7efdd', ink: '#7a5410', border: '#e8d6ad' }, // 琥珀
  { bg: '#f0ecfa', ink: '#5b4a9e', border: '#d9d0f0' }, // 紫
  { bg: '#fbeaf0', ink: '#9c3b62', border: '#f0cddc' }, // 玫红
  { bg: '#e9eef8', ink: '#3a4e8c', border: '#cdd7ef' }, // 靛
  { bg: '#f2efe6', ink: '#6b5b34', border: '#ded6bc' }  // 橄榄
];
const neutralTagStyle = {
  backgroundColor: 'var(--color-subtle)',
  color: 'var(--color-ink-2)',
  borderColor: 'var(--color-line)'
};

// 规格 -> 色板下标，保证同一规格始终同色，并尽量让不同规格不撞色
const specColorMap = new Map<string, number>();
const hashString = (value: string) => {
  let hash = 5381;
  for (let i = 0; i < value.length; i++) {
    hash = ((hash << 5) + hash + value.charCodeAt(i)) >>> 0;
  }
  return hash;
};

const getSpecTagStyle = (spec: string) => {
  const key = spec.trim();
  if (!key || key === '-') return neutralTagStyle;

  if (!specColorMap.has(key)) {
    const used = new Set(specColorMap.values());
    let index = hashString(key) % specPalette.length;
    for (let i = 0; i < specPalette.length && used.has(index); i++) {
      index = (index + 1) % specPalette.length;
    }
    specColorMap.set(key, index);
  }

  const tone = specPalette[specColorMap.get(key)!];
  return { backgroundColor: tone.bg, color: tone.ink, borderColor: tone.border };
};

// 后端「商品规格」形如：【香水柠檬5斤】,【广香一级10斤】
// 兼容纯文本分隔、以及 [{name, weight}] 结构化数据
const splitSpecs = (raw: unknown): string[] => {
  if (Array.isArray(raw)) {
    const list = raw
      .map(entry => {
        if (entry && typeof entry === 'object') {
          const item = entry as { name?: string; weight?: number | string };
          return `${item.name || ''}${item.weight ?? ''}`.trim();
        }
        return String(entry ?? '').trim();
      })
      .filter(Boolean);
    return list.length ? list : ['-'];
  }

  const text = String(raw ?? '').trim();
  if (!text || text === '-') return ['-'];

  const bracketed = text.match(/【([^】]+)】/g);
  if (bracketed) {
    const list = bracketed.map(part => part.slice(1, -1).trim()).filter(Boolean);
    if (list.length) return list;
  }
  const list = text.split(/[,，、;；\n\/]+/).map(part => part.trim()).filter(Boolean);
  return list.length ? list : [text];
};

const resetQuery = async () => {
  // 重置分页参数
  currentPage.value = 1;
  pageSize.value = 10;

  // 重置搜索参数
  searchParams.value = {
    name: '',
    phone: '',
    address: '',
    item: '',
    keyword: '',
    startTime: '',
    endTime: ''
  };

  // 同步清空时间筛选控件与快捷项状态
  dateRange.value = null;
  activeShortcut.value = '';
  errors.value.dateRange = '';

  // 调用 fetchOrderData 获取数据
  await fetchOrderData();
};
</script>

<template>
  <div class="space-y-6">
    <div class="flex items-center justify-between">
      <h2 class="text-ink font-semibold text-xl">订单详情管理</h2>
      <span class="bg-subtle text-ink-2 rounded-full px-3 py-1 text-xs">共计 {{ totalOrders }} 条</span>
    </div>

    <!-- Advanced Query Section -->
    <section class="bg-surface border border-line rounded-lg p-5 shadow-card">
      <div class="flex items-center justify-between mb-5">
        <div class="flex items-center gap-3">
          <div class="w-1.5 h-6 bg-brand rounded-full"></div>
          <h3 class="text-lg font-bold text-ink">高级查询筛选</h3>
        </div>
        <div class="flex items-center gap-3">
          <span class="text-xs text-ink-3 font-medium tracking-wide">共计 {{ totalOrders }} 条订单记录</span>
          <button 
            @click="toggleQuery"
            class="flex items-center gap-2 p-2 px-3 rounded-md hover:bg-hover text-ink-2 hover:text-ink transition-colors text-xs font-medium border border-line active:scale-95"
          >
            <span>{{ isExpanded ? '收起筛选' : '展开筛选' }}</span>
            <ChevronDown 
              class="w-4 h-4 transition-transform duration-300" 
              :class="{ 'rotate-180': isExpanded }"
            />
          </button>
        </div>
      </div>
      
      <div 
        class="transition-all duration-300 ease-in-out overflow-hidden" 
        :style="{ maxHeight: isExpanded ? '520px' : '0px', opacity: isExpanded ? '1' : '0', marginTop: isExpanded ? '0px' : '-15px' }"
      >
        <div class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-5 items-start">

          <div class="space-y-2">
            <label class="filter-label">
              客户姓名
            </label>
            <div class="relative">
              <User class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-brand/60" />
              <input type="text" v-model="searchParams.name" placeholder="搜索姓名..." class="w-full pl-9 pr-3 h-10 rounded-md border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:border-brand transition-colors">
            </div>
          </div>
          
          <div class="space-y-2">
            <label class="filter-label">
              联系电话
            </label>
            <div class="relative">
              <Phone class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-brand/60" />
              <input type="text" v-model="searchParams.phone" placeholder="手机号或固话..." class="w-full pl-9 pr-3 h-10 rounded-md border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:border-brand transition-colors">
            </div>
          </div>
          
          <div class="space-y-2">
            <label class="filter-label">
              收货地址
            </label>
            <div class="relative">
              <MapPin class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-brand/60" />
              <input type="text" v-model="searchParams.address" placeholder="省、市、区、具体地址..." class="w-full pl-9 pr-3 h-10 rounded-md border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:border-brand transition-colors">
            </div>
          </div>

          
          <div class="space-y-2 col-span-1 md:col-span-2">
            <label class="filter-label">
              日期范围
            </label>
            <div class="relative date-range-field">
              <ElDatePicker
                v-model="dateRange"
                type="daterange"
                range-separator="至"
                start-placeholder="开始日期"
                end-placeholder="结束日期"
                class="w-full"
                :popper-class="'date-picker-popper'"
                :clearable="true"
                size="large"
                @change="handleDateChange"
              />
            </div>
          </div>

          <div class="space-y-2">
            <label class="filter-label">
              关键词搜索
            </label>
            <div class="relative">
              <Search class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-brand/60" />
              <input type="text" v-model="searchParams.keyword" @input="handleKeywordSearch" placeholder="输入关键词搜索..." class="w-full pl-9 pr-10 h-10 rounded-md border border-line-strong bg-surface text-ink text-sm focus:outline-none focus:border-brand transition-colors">
              <button v-if="searchParams.keyword" @click="searchParams.keyword = ''" class="absolute right-3 top-1/2 -translate-y-1/2 text-ink-3 hover:text-ink transition-colors">
                &times;
              </button>
            </div>
          </div>

          <!-- 快捷时间：与上方筛选控件左边缘对齐 -->
          <div class="space-y-2 col-span-full">
            <label class="filter-label">
              <Calendar class="w-3.5 h-3.5 text-brand/60" />
              快捷时间
            </label>
            <div class="flex flex-wrap items-center gap-2">
              <button
                v-for="shortcut in dateShortcuts"
                :key="shortcut.key"
                @click="applyDateShortcut(shortcut)"
                class="h-8 px-3 rounded-md border text-xs font-medium transition-colors active:scale-95"
                :class="activeShortcut === shortcut.key
                  ? 'bg-brand-soft border-brand-border text-brand-ink'
                  : 'bg-surface border-line-strong text-ink-2 hover:bg-hover'"
              >
                {{ shortcut.label }}
              </button>
              <button
                @click="clearDateRange"
                class="h-8 px-3 rounded-md border border-line text-ink-3 hover:bg-hover hover:text-ink text-xs font-medium transition-colors active:scale-95"
              >
                清空时间
              </button>
              <span v-if="searchParams.startTime && searchParams.endTime" class="ml-auto font-mono text-xs text-ink-3">
                {{ searchParams.startTime }} 至 {{ searchParams.endTime }}
              </span>
            </div>
          </div>

          <!-- 错误提示 -->
          <div v-if="errors.dateRange" class="col-span-full">
            <p class="text-danger text-xs font-medium">{{ errors.dateRange }}</p>
          </div>
        </div>
      </div>
      
      <div class="flex flex-wrap items-center justify-end gap-3 mt-6 pt-5 border-t border-line">
        <button
          @click="exportToExcel"
          :disabled="orderDetails.length === 0"
          class="h-9 px-4 rounded-md border border-line-strong text-ink-2 hover:bg-hover font-medium transition-colors flex items-center gap-1.5 active:scale-95 text-xs disabled:opacity-50 disabled:cursor-not-allowed"
        >
          批量导出 <Download class="w-3.5 h-3.5" />
        </button>
        <button @click="resetQuery" class="h-9 px-4 rounded-md border border-line-strong text-ink-2 hover:bg-hover font-medium transition-colors flex items-center gap-1.5 active:scale-95 text-xs">
          <RotateCcw class="w-3.5 h-3.5" /> 重置筛选
        </button>
        <button @click="fetchOrderData" class="h-9 px-6 rounded-md bg-brand hover:bg-brand-hover text-onbrand font-medium shadow-pop transition-colors flex items-center gap-1.5 active:scale-95 text-xs">
          <Search class="w-3.5 h-3.5" /> 立即查询
        </button>
      </div>
    </section>

    <!-- Table Section -->
    <section class="bg-surface border border-line rounded-lg overflow-hidden shadow-card relative">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-sm border-collapse">
          <thead class="bg-subtle text-ink-2 font-medium">
            <tr class="border-b border-line">
              <th class="px-6 py-5">创建时间</th>
              <th class="px-6 py-5">客户姓名</th>
              <th class="px-6 py-5">联系电话</th>
              <th class="px-6 py-5">收货地址</th>
              <th class="px-6 py-5">规格信息</th>
              <th class="px-6 py-5">备注</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="(order, index) in orderDetails" :key="index" class="table-row-hover transition-colors" :class="{ 'bg-subtle/50': index % 2 === 1 }">
              <td class="px-6 py-5 text-ink-3 text-xs font-mono whitespace-nowrap">{{ order.time }}</td>
              <td class="px-6 py-5 font-medium text-ink">{{ order.name }}</td>
              <td class="px-6 py-5 font-mono text-ink-2">{{ order.phone }}</td>
              <td class="px-6 py-5 text-ink-2 max-w-[200px]">{{ order.address }}</td>

              <td class="px-6 py-5 max-w-[280px]">
                <div class="flex flex-wrap items-center gap-1.5">
                  <span
                    v-for="(spec, specIndex) in splitSpecs(order.item)"
                    :key="specIndex"
                    class="inline-flex items-center px-2.5 py-1 rounded-md border text-xs font-medium whitespace-nowrap"
                    :style="getSpecTagStyle(spec)"
                  >
                    {{ spec }}
                  </span>
                </div>
              </td>
              <td class="px-6 py-5 font-medium text-ink">{{ order.remark }}</td>
            </tr>
          </tbody>
        </table>
      </div>
      
      <!-- Pagination -->
      <div class="p-6 border-t border-line flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
        <div class="flex flex-col sm:flex-row items-start sm:items-center gap-4 w-full sm:w-auto">
          <!-- 每页显示条数选择器 -->
          <div class="flex items-center gap-2">
            <label class="text-xs text-ink-3 font-medium">每页显示：</label>
            <select 
              v-model.number="pageSize" 
              @change="changePageSize(pageSize)"
              class="px-3 py-2 rounded-md border border-line-strong bg-surface text-ink text-xs transition-colors focus:outline-none focus:border-brand"
            >
              <option v-for="option in pageSizeOptions" :key="option" :value="option">{{ option }}</option>
            </select>
          </div>
          
          <!-- 显示信息 -->
          <p class="text-xs text-ink-3 font-medium">
            显示 {{ (currentPage - 1) * pageSize + 1 }} 到 {{ Math.min(currentPage * pageSize, totalOrders) }} 条，共 {{ totalOrders }} 条记录
          </p>
        </div>
        
        <!-- 页码导航 -->
        <div class="flex items-center gap-2 w-full sm:w-auto justify-center sm:justify-end">
          <!-- 上一页按钮 -->
          <button 
            @click="prevPage"
            :disabled="currentPage === 1"
            class="w-8 h-8 flex items-center justify-center rounded-md border border-line text-ink-2 hover:bg-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <ChevronLeft class="w-4 h-4" />
          </button>
          
          <!-- 页码按钮 -->
          <template v-for="(item, idx) in displayPages" :key="idx">
            <button
              v-if="item.type === 'page'"
              @click="changePage(item.value)"
              class="w-8 h-8 flex items-center justify-center rounded-md text-xs font-bold transition-colors"
              :class="currentPage === item.value ? 'bg-brand text-onbrand' : 'border border-line text-ink-2 hover:bg-hover'"
            >
              {{ item.value }}
            </button>
            <span v-else class="text-ink-3 text-xs mx-1 px-1 select-none">...</span>
          </template>
          
          <!-- 下一页按钮 -->
          <button 
            @click="nextPage"
            :disabled="currentPage === totalPages"
            class="w-8 h-8 flex items-center justify-center rounded-md border border-line text-ink-2 hover:bg-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <ChevronRight class="w-4 h-4" />
          </button>
        </div>
      </div>
      
      <!-- 加载状态 -->
      <div v-if="isLoading" class="absolute inset-0 bg-app/70 flex items-center justify-center z-10">
        <div class="bg-surface border border-line rounded-modal p-8 flex flex-col items-center gap-4 shadow-pop">
          <div class="w-12 h-12 border-4 border-brand border-t-transparent rounded-full animate-spin"></div>
          <p class="text-ink-2 font-medium">加载中...</p>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.table-row-hover:hover {
  background-color: var(--color-hover);
}

/* 所有筛选项的标签基线一致，保证控件上下对齐 */
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

/* 日期区间控件与普通输入框统一为 40px 高、清爽描边 */
.date-range-field :deep(.el-date-editor) {
  width: 100%;
  height: 40px;
  box-sizing: border-box;
  padding: 0 12px;
}
.date-range-field :deep(.el-date-editor.is-active) {
  box-shadow: 0 0 0 1px var(--color-brand) inset,
    0 0 0 3px rgba(45, 100, 184, 0.12) !important;
}
.date-range-field :deep(.el-range-input) {
  font-size: 14px;
  color: var(--color-ink);
}
.date-range-field :deep(.el-range-input::placeholder) {
  color: var(--color-ink-3);
}
.date-range-field :deep(.el-range__icon) {
  color: var(--color-brand);
  opacity: 0.7;
}
.date-range-field :deep(.el-range-separator) {
  color: var(--color-ink-3);
  font-size: 13px;
}
</style>
