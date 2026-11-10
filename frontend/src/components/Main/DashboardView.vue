<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue';
import * as echarts from 'echarts';
// 中国省级边界 GeoJSON（DataV，省级），离线打包，地图省份名需与后端返回的省份全名一致
import chinaJson from '../../assets/china.json';
import { GetDashboard, RefreshDashboardData } from '../../../wailsjs/go/main/App';

echarts.registerMap('china', chinaJson as any);

interface TrendPoint { key: string; orders: number; weight: number }
interface NameValue { name: string; value: number; orders: number }
interface CustomerValue { name: string; orders: number; weight: number }
interface ProvinceCount { name: string; orders: number }
interface DashboardData {
  summary: {
    totalOrders: number;
    totalWeight: number;
    repurchaseRate: number;
    customerCount: number;
    productCount: number;
    unknownProvince: number;
  };
  trend: TrendPoint[];
  topProducts: NameValue[];
  topCustomers: CustomerValue[];
  province: ProvinceCount[];
  // 未命中 cfg_product_belongs 的商品（按斤降序），仅用于提示，不进爆品榜
  unmappedProducts: NameValue[];
  // false 表示商品归属配置未加载，此时爆品榜退回原始品名
  belongReady: boolean;
  generatedAt: string;
  loading?: boolean;
}

type Period = 'day' | 'week' | 'month';
type RangeKey = 'all' | '30' | '90' | 'year';

const period = ref<Period>('day');
const range = ref<RangeKey>('all');
const loading = ref(false);
const hasData = ref(false);
const data = ref<DashboardData | null>(null);

// 未配置商品的汇总提示：这些斤数计入总销量，但不计入爆品榜
const unmapped = computed(() => {
  const list = data.value?.unmappedProducts || [];
  const weight = Math.round(list.reduce((sum, i) => sum + i.value, 0) * 10) / 10;
  const names = list.slice(0, 6).map((i) => `${i.name} ${i.value}斤`).join('、');
  return {
    count: list.length,
    weight,
    detail: names + (list.length > 6 ? ' 等' : ''),
  };
});

const trendEl = ref<HTMLElement>();
const productEl = ref<HTMLElement>();
const customerEl = ref<HTMLElement>();
const mapEl = ref<HTMLElement>();

let trendChart: echarts.ECharts | null = null;
let productChart: echarts.ECharts | null = null;
let customerChart: echarts.ECharts | null = null;
let mapChart: echarts.ECharts | null = null;

// 首屏轮询定时器（数据未就绪时轮询）与实时刷新定时器（后台每 10s 更新内存）
let pollTimer: number | null = null;
let liveTimer: number | null = null;

const periodOptions: { key: Period; label: string }[] = [
  { key: 'day', label: '按日' },
  { key: 'week', label: '按周' },
  { key: 'month', label: '按月' },
];
const rangeOptions: { key: RangeKey; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: '30', label: '近 30 天' },
  { key: '90', label: '近 90 天' },
  { key: 'year', label: '今年' },
];

// 手机号打码（内部工具，保留首尾便于核对）
function maskName(name: string): string {
  const t = name.trim();
  if (/^\d{7,}$/.test(t)) {
    return t.slice(0, 3) + '****' + t.slice(-4);
  }
  return t;
}

// 组装查询条件（周期 + 可选时间范围）
function buildPayload(): Record<string, any> {
  const payload: Record<string, any> = { period: period.value };
  if (range.value !== 'all') {
    const now = new Date();
    let start: Date;
    if (range.value === '30') start = new Date(now.getTime() - 30 * 86400000);
    else if (range.value === '90') start = new Date(now.getTime() - 90 * 86400000);
    else start = new Date(now.getFullYear(), 0, 1);
    payload.start = start.toISOString();
    payload.end = now.toISOString();
  }
  return payload;
}

// 取一次看板数据（只读后端内存缓存，不查库）。
// changed=数据版本变化已更新 / unchanged=版本没变（不该重绘）/ loading=预热未完成 / error=调用失败。
// force=true 用于切换周期/范围/手动刷新，无视版本变化强制更新。
async function fetchOnce(force = false): Promise<'changed' | 'unchanged' | 'loading' | 'error'> {
  try {
    // buildPayload 里的 start/end 传 ISO 字符串（Go time.Time 可直接解析），
    // 与生成的 DashboardRequest 类型声明不完全一致，运行时按 JSON 传参即可。
    const resp = (await GetDashboard(buildPayload() as any)) as any;
    if (resp && resp.loading) {
      return 'loading';
    }
    if (resp && resp.summary) {
      if (!force && data.value && resp.generatedAt === data.value.generatedAt) {
        return 'unchanged';
      }
      data.value = resp as DashboardData;
      hasData.value = resp.summary.totalOrders > 0;
      return 'changed';
    }
  } catch (e) {
    console.error('看板数据加载失败', e);
  }
  return 'error';
}

// 首屏取数：数据由首页 WarmupDashboard 提前异步预热，正常路径一次就到位；
// 这里不触发任何加载，只在预热尚未完成的极端情况下轮询，期间不阻塞界面。
async function load() {
  loading.value = true;
  const state = await fetchOnce(true);
  if (state === 'changed') {
    loading.value = false;
    await nextTick();
    renderCharts();
    return;
  }
  // 预热尚未完成或调用异常，1.5s 后再试
  pollTimer = window.setTimeout(load, 1500);
}

function renderCharts() {
  if (!data.value) return;
  renderTrend();
  renderProducts();
  renderCustomers();
  renderMap();
}

function ensureChart(el: HTMLElement | undefined, prev: echarts.ECharts | null) {
  if (!el) return null;
  if (prev) {
    prev.resize();
    return prev;
  }
  return echarts.init(el);
}

function renderTrend() {
  if (!trendEl.value) return;
  trendChart = ensureChart(trendEl.value, trendChart);
  if (!trendChart) return;
  const trend = data.value!.trend;
  const keys = trend.map((t) => t.key);
  trendChart.setOption(
    {
      tooltip: { trigger: 'axis' },
      legend: { data: ['订单量', '销量(斤)'], top: 0, textStyle: { color: '#5c6168' } },
      grid: { left: 48, right: 48, top: 40, bottom: 30 },
      xAxis: { type: 'category', data: keys, axisLabel: { color: '#8b9096' }, axisLine: { lineStyle: { color: '#e4e4df' } } },
      yAxis: [
        { type: 'value', name: '订单量', axisLabel: { color: '#8b9096' }, splitLine: { lineStyle: { color: '#edece8' } } },
        { type: 'value', name: '斤', axisLabel: { color: '#8b9096' }, splitLine: { show: false } },
      ],
      series: [
        { name: '订单量', type: 'bar', data: trend.map((t) => t.orders), itemStyle: { color: '#9cc2ec', borderRadius: [3, 3, 0, 0] }, barMaxWidth: 22 },
        { name: '销量(斤)', type: 'line', yAxisIndex: 1, smooth: true, data: trend.map((t) => t.weight), itemStyle: { color: '#2d64b8' }, lineStyle: { width: 2 } },
      ],
    },
    true,
  );
}

function renderProducts() {
  if (!productEl.value) return;
  // 全部商品都未命中归属配置时展示空态，不要在隐藏容器里初始化图表
  if (!data.value?.topProducts?.length) {
    productChart?.clear();
    return;
  }
  productChart = ensureChart(productEl.value, productChart);
  if (!productChart) return;
  const items = [...data.value.topProducts].reverse();
  productChart.setOption(
    {
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, formatter: (p: any) => `${p[0].name}<br/>销量 ${p[0].value} 斤 · ${p[0].data.orders} 条规格` },
      grid: { left: 8, right: 24, top: 16, bottom: 8, containLabel: true },
      xAxis: { type: 'value', axisLabel: { color: '#8b9096' }, splitLine: { lineStyle: { color: '#edece8' } } },
      yAxis: { type: 'category', data: items.map((i) => i.name), axisLabel: { color: '#5c6168' } },
      series: [
        {
          type: 'bar',
          data: items.map((i) => ({ value: i.value, orders: i.orders })),
          itemStyle: { color: '#2d64b8', borderRadius: [0, 3, 3, 0] },
          barMaxWidth: 18,
          label: { show: true, position: 'right', color: '#5c6168', formatter: '{c} 斤' },
        },
      ],
    },
    true,
  );
}

function renderCustomers() {
  if (!customerEl.value) return;
  customerChart = ensureChart(customerEl.value, customerChart);
  if (!customerChart) return;
  const items = [...(data.value!.topCustomers || [])].reverse();
  customerChart.setOption(
    {
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, formatter: (p: any) => `${maskName(p[0].name)}<br/>下单 ${p[0].value} 次` },
      grid: { left: 8, right: 24, top: 16, bottom: 8, containLabel: true },
      xAxis: { type: 'value', axisLabel: { color: '#8b9096' }, splitLine: { lineStyle: { color: '#edece8' } } },
      yAxis: { type: 'category', data: items.map((i) => maskName(i.name)), axisLabel: { color: '#5c6168' } },
      series: [
        {
          type: 'bar',
          data: items.map((i) => i.orders),
          itemStyle: { color: '#5c6168', borderRadius: [0, 3, 3, 0] },
          barMaxWidth: 18,
          label: { show: true, position: 'right', color: '#5c6168', formatter: '{c} 单' },
        },
      ],
    },
    true,
  );
}

function renderMap() {
  if (!mapEl.value) return;
  mapChart = ensureChart(mapEl.value, mapChart);
  if (!mapChart) return;
  const province = data.value!.province || [];
  const maxVal = province.reduce((m, p) => Math.max(m, p.orders), 0);
  mapChart.setOption(
    {
      tooltip: { trigger: 'item', formatter: (p: any) => `${p.name}<br/>订单 ${p.value ?? 0} 笔` },
      visualMap: {
        min: 0,
        max: Math.max(maxVal, 1),
        left: 16,
        bottom: 16,
        text: ['多', '少'],
        calculable: true,
        inRange: { color: ['#edf3fb', '#9cc2ec', '#2d64b8', '#1e4785'] },
        textStyle: { color: '#5c6168' },
      },
      series: [
        {
          type: 'map',
          map: 'china',
          roam: false,
          label: { show: false },
          itemStyle: { borderColor: '#ffffff', borderWidth: 0.5 },
          emphasis: { label: { show: true, color: '#2b2f33' }, itemStyle: { areaColor: '#24539b' } },
          data: province.map((p) => ({ name: p.name, value: p.orders })),
        },
      ],
    },
    true,
  );
}

function onResize() {
  trendChart?.resize();
  productChart?.resize();
  customerChart?.resize();
  mapChart?.resize();
}

// 切换周期/范围：数据已在内存 + 聚合结果缓存，秒开。
async function refresh() {
  const state = await fetchOnce(true);
  if (state === 'changed') {
    await nextTick();
    renderCharts();
  }
}

const manualBusy = ref(false);

// 手动刷新：只让后端做一次索引增量同步（绝不重扫全表），再等数据版本变化。
async function manualRefresh() {
  if (manualBusy.value) return;
  manualBusy.value = true;
  try {
    await RefreshDashboardData();
    let changed = false;
    for (let i = 0; i < 10; i++) {
      await new Promise<void>((resolve) => { window.setTimeout(resolve, 500); });
      if ((await fetchOnce(false)) === 'changed') {
        changed = true;
        break;
      }
    }
    if (!changed) await fetchOnce(true);
    await nextTick();
    renderCharts();
  } catch (e) {
    console.error('看板刷新失败', e);
  } finally {
    manualBusy.value = false;
  }
}

// 实时检查：每 10s 只问一次数据版本（纯内存、命中聚合缓存），
// generatedAt 没变就不重绘，避免旧实现里每 10s 全量重画 4 张图（含地图）。
async function silentRefresh() {
  if ((await fetchOnce(false)) === 'changed') {
    await nextTick();
    renderCharts();
  }
}

// 每 10s 检查一次数据版本：只是内存 IPC，不产生任何 SQL。
function startLive() {
  stopLive();
  liveTimer = window.setInterval(silentRefresh, 10000);
}
function stopLive() {
  if (liveTimer !== null) {
    clearInterval(liveTimer);
    liveTimer = null;
  }
}
function stopPoll() {
  if (pollTimer !== null) {
    clearTimeout(pollTimer);
    pollTimer = null;
  }
}

onMounted(() => {
  window.addEventListener('resize', onResize);
  // 首页挂载时已调用 WarmupDashboard 提前异步预热，这里只取内存缓存，不触发加载。
  load();
  startLive();
});

onUnmounted(() => {
  window.removeEventListener('resize', onResize);
  stopPoll();
  stopLive();
  trendChart?.dispose();
  productChart?.dispose();
  customerChart?.dispose();
  mapChart?.dispose();
});
</script>

<template>
  <div class="flex flex-col gap-4 p-1">
    <!-- 头部：标题 + 筛选 -->
    <div class="flex flex-wrap items-center justify-between gap-3">
      <div>
        <h1 class="text-xl font-bold text-ink">经营看板</h1>
        <p class="text-xs text-ink-3 mt-0.5">
          老板视角总览 · 数据来自已确认入库的订单
          <span v-if="data?.generatedAt"> · 更新于 {{ data.generatedAt }}</span>
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <!-- 周期 -->
        <div class="inline-flex rounded-md border border-line overflow-hidden">
          <button
            v-for="opt in periodOptions"
            :key="opt.key"
            @click="period = opt.key; refresh()"
            class="px-3 py-1.5 text-sm transition-colors"
            :class="period === opt.key ? 'bg-brand text-onbrand' : 'text-ink-2 hover:bg-hover'"
          >
            {{ opt.label }}
          </button>
        </div>
        <!-- 时间范围 -->
        <div class="inline-flex rounded-md border border-line overflow-hidden">
          <button
            v-for="opt in rangeOptions"
            :key="opt.key"
            @click="range = opt.key; refresh()"
            class="px-3 py-1.5 text-sm transition-colors"
            :class="range === opt.key ? 'bg-brand-soft text-brand-ink font-medium' : 'text-ink-2 hover:bg-hover'"
          >
            {{ opt.label }}
          </button>
        </div>
        <button
          @click="manualRefresh()"
          :disabled="loading || manualBusy"
          class="px-3 py-1.5 text-sm rounded-md border border-line text-ink-2 hover:bg-hover disabled:opacity-50 transition-colors"
        >
          {{ manualBusy ? '同步中…' : (loading ? '加载中…' : '刷新') }}
        </button>
      </div>
    </div>

    <div v-if="loading && !hasData" class="surface-card p-10 text-center text-ink-3 text-sm">正在加载经营数据…</div>

    <div v-else-if="!hasData" class="surface-card p-10 text-center text-ink-3 text-sm">
      当前筛选范围内暂无订单，请调整时间范围，或先在「订单输入」确认入库后再来看板。
    </div>

    <template v-else>
      <!-- 概览卡 -->
      <div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
        <div class="surface-card p-4">
          <p class="text-xs text-ink-3">总订单数</p>
          <p class="text-2xl font-bold text-ink mt-1">{{ data?.summary.totalOrders }}</p>
        </div>
        <div class="surface-card p-4">
          <p class="text-xs text-ink-3">总销量（斤）<span class="text-ink-disabled">· 暂代销售额</span></p>
          <p class="text-2xl font-bold text-ink mt-1">{{ data?.summary.totalWeight }}</p>
        </div>
        <div class="surface-card p-4">
          <p class="text-xs text-ink-3">复购率</p>
          <p class="text-2xl font-bold text-ink mt-1">{{ ((data?.summary.repurchaseRate ?? 0) * 100).toFixed(0) }}%</p>
        </div>
        <div class="surface-card p-4">
          <p class="text-xs text-ink-3">客户数 / 一级品类数</p>
          <p class="text-2xl font-bold text-ink mt-1">{{ data?.summary.customerCount }} / {{ data?.summary.productCount }}</p>
        </div>
      </div>

      <!-- 趋势 -->
      <div class="surface-card p-4">
        <div class="flex items-center gap-2 mb-2">
          <div class="w-1 h-4 bg-brand rounded-full"></div>
          <h2 class="text-sm font-semibold text-ink">销量趋势（订单量 / 斤）</h2>
        </div>
        <div ref="trendEl" class="w-full" style="height: 320px"></div>
      </div>

      <!-- 爆品 / 客户 -->
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div class="surface-card p-4">
          <div class="flex items-center justify-between gap-2 mb-2">
            <div class="flex items-center gap-2">
              <div class="w-1 h-4 bg-brand rounded-full"></div>
              <h2 class="text-sm font-semibold text-ink">爆品 Top（按斤 · 一级属性）</h2>
            </div>
            <p v-if="data && !data.belongReady" class="text-xs text-warning-ink shrink-0" title="未读取到 cfg_product_belongs 配置，暂时按订单里的原始品名统计">
              商品归属配置未加载，暂按原始品名统计
            </p>
            <p v-else-if="unmapped.count" class="text-xs text-warning-ink shrink-0 cursor-help" :title="`未匹配商品归属配置：${unmapped.detail}。这些销量计入总销量，但未计入爆品榜，可在「账单结算」补充归属配置。`">
              另有 {{ unmapped.count }} 种未配置商品（{{ unmapped.weight }} 斤）未计入
            </p>
            <p v-else class="text-xs text-ink-disabled shrink-0">已按商品归属配置归并</p>
          </div>
          <div v-if="!data?.topProducts?.length" class="flex items-center justify-center text-xs text-ink-3" style="height: 300px">
            当前范围内没有命中商品归属配置的销量
          </div>
          <div v-show="data?.topProducts?.length" ref="productEl" class="w-full" style="height: 300px"></div>
        </div>
        <div class="surface-card p-4">
          <div class="flex items-center gap-2 mb-2">
            <div class="w-1 h-4 bg-brand rounded-full"></div>
            <h2 class="text-sm font-semibold text-ink">客户贡献 Top（下单次数）</h2>
          </div>
          <div ref="customerEl" class="w-full" style="height: 300px"></div>
        </div>
      </div>

      <!-- 中国地图：省份订单分布 -->
      <div class="surface-card p-4">
        <div class="flex items-center justify-between mb-2">
          <div class="flex items-center gap-2">
            <div class="w-1 h-4 bg-brand rounded-full"></div>
            <h2 class="text-sm font-semibold text-ink">各省份订单分布</h2>
          </div>
          <p v-if="data?.summary.unknownProvince" class="text-xs text-warning-ink">
            另有 {{ data.summary.unknownProvince }} 笔地址未解析出省份
          </p>
        </div>
        <div ref="mapEl" class="w-full" style="height: 520px"></div>
      </div>
    </template>
  </div>
</template>
