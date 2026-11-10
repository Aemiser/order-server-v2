<template>
  <div class="table-card surface-card">
    <div class="table-wrapper">
      <!-- 无规格列时不渲染只有一列的空表：否则第一列会被 100% 宽度撑满整屏 -->
      <div v-if="!hasSpecColumns" class="table-empty">
        <svg class="table-empty-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="3" y="4" width="18" height="16" rx="2" />
          <path d="M3 10h18M9 10v10M15 10v10" />
        </svg>
        <p class="table-empty-title">暂无账单数据</p>
        <p class="table-empty-hint">选择日期范围后点击「查询」，即可生成结算表格</p>
      </div>
      <table v-else :style="tableStyle">
        <colgroup>
          <col :style="{ width: labelColWidth }" />
          <col v-for="n in specColumnCount" :key="n" />
        </colgroup>
        <thead>
          <tr class="product-group">
            <th class="group-label">日期 / 产品</th>
            <th v-for="product in products" :key="product.name" :colspan="product.specs.length" class="group-header border-l border-line-strong">
              {{ product.name }}
            </th>
          </tr>
          <tr class="spec-header">
            <th class="group-label"></th>
            <template v-for="product in products" :key="product.name">
              <th v-for="spec in product.specs" :key="`${product.name}-${specLabel(spec)}`" class="border-l border-line-subtle">
                {{ specLabel(spec) }}
              </th>
            </template>
          </tr>
        </thead>
        <tbody>
          <!-- 使用v-for动态渲染数据行 -->
          <tr v-for="(row, index) in tableData" :key="index" class="data-row">
            <td class="date-column">{{ row.date }}</td>
            <template v-for="(product, productIndex) in products" :key="product.name">
              <td v-for="(spec, specIndex) in product.specs" :key="`${productIndex}-${specIndex}`" class="spec-cell border-l border-line-subtle">
                <template v-if="row.values && row.values[getSpecIndex(productIndex, specIndex)] !== '—'">
                  <span class="spec-value">{{ row.values[getSpecIndex(productIndex, specIndex)] }}</span>
                </template>
                <template v-else>
                  <span class="empty-cell">—</span>
                </template>
              </td>
            </template>
          </tr>

          <!-- 总计行 -->
          <tr class="summary-row">
            <td class="summary-label">总计 (件)</td>
            <template v-for="(product, productIndex) in products" :key="product.name">
              <td v-for="(spec, specIndex) in product.specs" :key="`total-${productIndex}-${specIndex}`" class="border-l border-line-subtle">
                {{ totalRow[getSpecIndex(productIndex, specIndex)] }}
              </td>
            </template>
          </tr>

          <!-- 元/斤行 -->
          <tr class="summary-row">
            <td class="summary-label">
              <template v-if="isEditingUnit">
                <div class="flex items-center gap-1.5">
                  <input 
                    type="text" 
                    v-model="editUnit"
                    @blur="saveUnit"
                    @keyup.enter="saveUnit"
                    @keyup.esc="cancelEditUnit"
                    class="w-20 bg-surface border border-line-strong rounded px-3 py-1 text-ink focus:outline-none focus:border-brand transition-colors text-sm"
                    placeholder="输入计量单位"
                  />
                  <button 
                    @click="saveUnit"
                    class="p-1 rounded border border-line text-ink-2 hover:bg-hover hover:text-success transition-colors"
                    title="保存"
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 13l4 4L19 7" />
                    </svg>
                  </button>
                  <button 
                    @click="cancelEditUnit"
                    class="p-1 rounded border border-line text-ink-2 hover:bg-hover hover:text-ink transition-colors"
                    title="取消"
                  >
                    <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M6 18L18 6M6 6l12 12" />
                    </svg>
                  </button>
                </div>
              </template>
              <template v-else>
                <span @click="startEditUnit" class="cursor-pointer hover:text-brand transition-colors">
                  {{ unitOfMeasure }}
                </span>
              </template>
            </td>
            <template v-for="(product, productIndex) in products" :key="product.name">
              <td :colspan="product.specs.length" class="border-l border-line-strong">
                <template v-if="isEditMode">
                  <input 
                    type="text" 
                    :value="productPrices[productIndex] || ''"
                    @input="handleProductPriceChange(productIndex, ($event.target as HTMLInputElement).value)"
                    class="w-full bg-surface border border-line-strong rounded px-3 py-2 text-ink focus:outline-none focus:border-brand transition-colors text-center"
                    placeholder="0.00"
                  />
                </template>
                <template v-else>
                  <div class="text-center py-3.5">{{ pricePerKgRow[getSpecIndex(productIndex, 0)] }}</div>
                </template>
              </td>
            </template>
          </tr>

          <!-- 元/件行 -->
          <tr class="summary-row">
            <td class="summary-label">元/件</td>
            <template v-for="(product, productIndex) in products" :key="product.name">
              <td v-for="(spec, specIndex) in product.specs" :key="`price-${productIndex}-${specIndex}`" class="border-l border-line-subtle">
                <template v-if="isEditMode">
                  <input 
                    type="text" 
                    :value="editPrices[getSpecIndex(productIndex, specIndex)] || ''"
                    @input="handlePriceChange(getSpecIndex(productIndex, specIndex), ($event.target as HTMLInputElement).value)"
                    class="w-full bg-surface border border-line-strong rounded px-3 py-2 text-ink focus:outline-none focus:border-brand transition-colors text-center"
                    placeholder="0.00"
                  />
                </template>
                <template v-else>
                  {{ pricePerItemRow[getSpecIndex(productIndex, specIndex)] }}
                </template>
              </td>
            </template>
          </tr>

          <!-- 金额行 -->
          <tr class="summary-row">
            <td class="summary-label">金额</td>
            <template v-for="(product, productIndex) in products" :key="product.name">
              <td v-for="(spec, specIndex) in product.specs" :key="`amount-${productIndex}-${specIndex}`" class="border-l border-line-subtle">
                {{ totalAmountRow[getSpecIndex(productIndex, specIndex)] }}
              </td>
            </template>
          </tr>

          <!-- 总金额行：金额行各规格金额之和 -->
          <tr class="conclusion-row grand-total-row">
            <td class="conclusion-label">总金额</td>
            <td :colspan="specColumnCount || 1" class="grand-total-cell border-l border-line-strong">
              <span class="grand-total-value">{{ grandTotalText }}</span>
              <span v-if="hasAmountData" class="grand-total-unit">元</span>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, watch, computed } from 'vue'

// 后端数据结构
// 后端 Wails 绑定里 Spec 是对象（带 Name），历史数据也可能是纯字符串，
// 两种形态都要兼容；显示名统一经 specLabel 取值。
interface SpecObject {
  name?: string | null;
  Name?: string | null;
}
type SpecValue = string | SpecObject;

interface ProSPecs {
  SID: number;
  Name: string;
  Specs: SpecValue[];
}

interface CheckoutResponse {
  FieldList: string[];
  Count: number;
  ProSPecs: ProSPecs[];
  TableData: string[];
  PBMap: Record<string, string[]>;
  TotalRow?: (number | string)[];
  PricePerKgRow?: (number | string)[];
  PricePerItemRow?: (number | string)[];
  // 后端 Wails 绑定返回大写字段，查询组件会映射为小写，两种都兼容
  TotalAmountRow?: (number | string)[];
  totalAmountRow?: (number | string)[];
}

// 前端表格数据结构
interface TableRow {
  date: string;
  values: (number | string)[];
}

// 商品数据结构
interface Product {
  name: string;
  specs: SpecValue[];
}

const specLabel = (spec: SpecValue | null | undefined): string => {
  if (spec == null) return '';
  if (typeof spec === 'string') return spec;
  return String(spec.name || spec.Name || JSON.stringify(spec));
};

// 预设颜色库
const colorPalette = [
  '#3B82F6', // 蓝色
  '#10B981', // 绿色
  '#F59E0B', // 橙色
  '#8B5CF6', // 紫色
  '#EC4899', // 粉色
  '#14B8A6', // 青绿色
  '#F43F5E', // 红色
  '#6366F1', // 靛蓝色
  '#06B6D4', // 青色
  '#F97316'  // 深橙色
];
// 存储已分配颜色的Map
const productColorMap = new Map<string, string>();
// 记录已使用的颜色索引
const usedColorIndices = new Set<number>();

// 根据商品名称生成哈希值
const getProductHash = (productName: string): number => {
  let hash = 0;
  for (let i = 0; i < productName.length; i++) {
    const char = productName.charCodeAt(i);
    hash = ((hash << 5) - hash) + char;
    hash = hash & hash; // 转换为32位整数
  }
  return Math.abs(hash);
};

const getProductColor = (productName: string): string => {
  // 如果已经为该产品分配过颜色，直接返回
  if (productColorMap.has(productName)) {
    return productColorMap.get(productName)!;
  }

  // 获取产品hash
  const hash = getProductHash(productName);

  // 尝试找到未使用的颜色
  let index = hash % colorPalette.length;
  let attempts = 0;
  const maxAttempts = colorPalette.length * 2; // 最大尝试次数

  // 如果当前颜色已被使用，尝试寻找下一个可用颜色
  while (usedColorIndices.has(index) && attempts < maxAttempts) {
    index = (index + 1) % colorPalette.length;
    attempts++;
  }

  // 获取颜色
  const color = colorPalette[index];

  // 记录已使用的颜色和产品映射
  usedColorIndices.add(index);
  productColorMap.set(productName, color);

  return color;
};

// 商品列表
const products = ref<Product[]>([
  { name: '香水柠檬', specs: ['5斤', '8斤', '10斤', '20斤'] },
  { name: '广香一级', specs: ['10斤', '20斤'] },
  { name: '广香二级', specs: ['7斤', '15斤'] },
  { name: '黄柠檬', specs: ['3斤', '6斤'] },
  { name: '金桔', specs: ['2斤', '4斤'] }
]);

// 表格数据
const tableData = ref<TableRow[]>([])

// 单价数据
const prices = ref<(number | string)[]>([])

// 后端返回的汇总数据
const totalRow = ref<(number | string)[]>([])
const pricePerKgRow = ref<(number | string)[]>([])
const pricePerItemRow = ref<(number | string)[]>([])
const totalAmountRow = ref<(number | string)[]>([])

// 规格列总数，总金额行按此跨列
const specColumnCount = computed(() =>
  products.value.reduce((count, product) => count + (product.specs?.length || 0), 0)
)

// ---- 列宽策略（按需调整这几个常量即可）----
// 第一列「日期 / 产品 / 总计 (件)」用 clamp 自适应：数据少时不会被撑满整屏，
// 规格列多时也不会无限变宽；其余列平分剩余宽度，并保证每列不低于最小阈值。
const LABEL_COL_MIN = 200      // 第一列下限（需容纳日期与「元/斤」编辑控件）
const LABEL_COL_MAX = 240      // 第一列上限
const LABEL_COL_IDEAL = '14vw' // 期望宽度随窗口自适应（<col> 上的百分比会被浏览器忽略，必须用视口单位）
const SPEC_COL_MIN = 110       // 每个规格列最小宽度（含千分位金额）

const labelColWidth = `clamp(${LABEL_COL_MIN}px, ${LABEL_COL_IDEAL}, ${LABEL_COL_MAX}px)`
const hasSpecColumns = computed(() => specColumnCount.value > 0)
const tableStyle = computed(() => ({
  width: '100%',
  minWidth: `max(100%, ${LABEL_COL_MIN + Math.max(specColumnCount.value, 1) * SPEC_COL_MIN}px)`,
}))

// 汇总金额行的所有金额：忽略空值、破折号等非数值内容，按分四舍五入避免浮点误差
const grandTotal = computed(() => {
  let sum = 0
  let hasValue = false
  totalAmountRow.value.slice(0, specColumnCount.value).forEach(raw => {
    const num = typeof raw === 'number' ? raw : parseFloat(String(raw ?? '').replace(/,/g, ''))
    if (Number.isFinite(num)) {
      sum += num
      hasValue = true
    }
  })
  return { sum: Math.round((sum + Number.EPSILON) * 100) / 100, hasValue }
})

const hasAmountData = computed(() => grandTotal.value.hasValue)

// 千分位 + 两位小数；无金额数据时显示占位符
const grandTotalText = computed(() =>
  hasAmountData.value
    ? grandTotal.value.sum.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 2 })
    : '—'
)

// 编辑模式状态
const isEditMode = ref(false)

// 本地编辑的价格数据
const editPrices = ref<(number | string)[]>([])
const editPricePerKg = ref<(number | string)[]>([])
const productPrices = ref<(number | string)[]>([])

// 计量单位状态
const unitOfMeasure = ref('元/斤')
const isEditingUnit = ref(false)
const editUnit = ref('元/斤')

// 模拟从后端获取数据
const fetchCheckoutData = async (): Promise<CheckoutResponse> => {
  // 实际项目中这里会调用API
  // const response = await fetch('/api/checkout');
  // return response.json();
  
  // 模拟API延迟
  return new Promise(resolve => {
    setTimeout(() => {
      resolve({
        FieldList: ['客户信息', '订单信息', '配送信息', '备注', '操作'],
        Count: 0,
        ProSPecs: [],
        TableData: [],
        PBMap: {
          '客户信息': ['客户姓名', '联系方式'],
          '配送信息': ['配送地址'],
          '订单要求': ['商品信息'],
          '备注说明': ['特殊要求'],
          '操作选项': ['打印选项']
        }
      });
    }, 500);
  });
};

// 数据转换函数：将后端数据转换为前端表格数据
const transformData = (data: CheckoutResponse): TableRow[] => {
  // 确保数据结构完整
  if (!data) {
    return [];
  }
  
  // 更新商品列表
  if (data.ProSPecs && data.ProSPecs.length > 0) {
    products.value = data.ProSPecs.map(proSpec => ({
      name: proSpec.Name || '',
      specs: (proSpec.Specs || []).map(spec => ({ name: specLabel(spec) }))
    }));
  } else {
    // 当没有商品数据时，清空商品列表
    products.value = [];
  }
  
  // 构建表格数据
  const tableRows: TableRow[] = [];
  
  // 解析 TableData
  if (data.TableData && data.TableData.length > 0) {
    // 假设 TableData 是按日期分组的字符串数组
    // 格式可能为: ["日期1,值1,值2,...", "日期2,值1,值2,..."]
    data.TableData.forEach(rowData => {
      if (typeof rowData === 'string') {
        const parts = rowData.split(',');
        if (parts.length > 0) {
          const date = parts[0] || '';
          const values = parts.slice(1).map(value => {
            // 尝试转换为数字，如果失败则保持字符串
            const numValue = parseFloat(value);
            return isNaN(numValue) ? value : numValue;
          });
          tableRows.push({ date, values });
        }
      }
    });
  }
  
  // 从后端数据中提取汇总数据
  totalRow.value = data.TotalRow || [];
  pricePerKgRow.value = data.PricePerKgRow || [];
  pricePerItemRow.value = data.PricePerItemRow || [];
  totalAmountRow.value = data.TotalAmountRow || data.totalAmountRow || [];
  
  // 更新单价数据
  prices.value = pricePerItemRow.value;
  
  return tableRows;
};

// 处理测试数据生成
const handleGenerateTestData = (testData: CheckoutResponse) => {
  console.log('收到测试数据:', testData);
  
  const transformedData = transformData(testData);
  tableData.value = transformedData;
  console.log('测试数据已转换并更新表格');
};

// 定义组件事件
const emit = defineEmits<{
  (e: 'generate-test-data', testData: CheckoutResponse): void;
  (e: 'save-prices', prices: (number | string)[]): void;
  (e: 'save-unit', unit: string): void;
  (e: 'enter-edit-mode'): void;
  (e: 'exit-edit-mode'): void;
  (e: 'clear-prices'): void;
  (e: 'data-status', hasData: boolean): void;
}>();

// 定义组件属性
const props = defineProps<{
  testData?: CheckoutResponse;
  checkoutResponse?: CheckoutResponse;
  isEditMode?: boolean;
}>();

// 监听编辑模式变化
watch(() => props.isEditMode, (newVal) => {
  if (newVal !== undefined) {
    if (!newVal && isEditMode.value) {
      // 退出编辑模式时，先保存价格数据
      savePrices();
    }
    
    isEditMode.value = newVal;
    if (newVal) {
      // 进入编辑模式时初始化编辑价格数据
      editPrices.value = [...pricePerItemRow.value];
      editPricePerKg.value = [...pricePerKgRow.value];
      
      // 初始化商品价格数组，每个商品一个价格
      productPrices.value = [];
      let specIndex = 0;
      products.value.forEach(product => {
        // 取该商品第一个规格的价格作为默认值
        if (product.specs.length > 0 && editPricePerKg.value[specIndex]) {
          productPrices.value.push(editPricePerKg.value[specIndex]);
        } else {
          productPrices.value.push('');
        }
        specIndex += product.specs.length;
      });
    } else {
      // 退出编辑模式时重置编辑价格数据
      editPrices.value = [];
      editPricePerKg.value = [];
      productPrices.value = [];
    }
  }
}, { immediate: true });

// 监听后端响应数据变化
watch(() => props.checkoutResponse, (newVal) => {
  if (newVal) {
    console.log('收到后端响应数据:', newVal);
    // 更新表格数据
    const transformedData = transformData(newVal);
    tableData.value = transformedData;
    // 更新汇总数据
    totalRow.value = newVal.TotalRow || [];
    pricePerKgRow.value = newVal.PricePerKgRow || [];
    pricePerItemRow.value = newVal.PricePerItemRow || [];
    totalAmountRow.value = newVal.TotalAmountRow || newVal.totalAmountRow || [];
    
    // 如果当前处于编辑模式，重新初始化productPrices数组
    if (isEditMode.value) {
      productPrices.value = [];
      let specIndex = 0;
      products.value.forEach(product => {
        // 取该商品第一个规格的价格作为默认值
        if (product.specs.length > 0 && pricePerKgRow.value[specIndex]) {
          productPrices.value.push(pricePerKgRow.value[specIndex]);
        } else {
          productPrices.value.push('');
        }
        specIndex += product.specs.length;
      });
    }
  }
}, { deep: true });

// 计算规格在一维数组中的索引
const getSpecIndex = (productIndex: number, specIndex: number): number => {
  let index = 0;
  for (let i = 0; i < productIndex; i++) {
    if (products.value[i] && products.value[i].specs) {
      index += products.value[i].specs.length;
    }
  }
  return index + specIndex;
};

// 进入编辑模式
const enterEditMode = () => {
  isEditMode.value = true;
  // 初始化编辑价格数据
  editPrices.value = [...pricePerItemRow.value];
  editPricePerKg.value = [...pricePerKgRow.value];
};

// 退出编辑模式
const exitEditMode = () => {
  isEditMode.value = false;
  // 重置编辑价格数据
  editPrices.value = [];
  editPricePerKg.value = [];
};

// 处理商品价格变化
const handleProductPriceChange = (productIndex: number, value: string) => {
  // 只允许输入数字和小数点
  const numericValue = value.replace(/[^0-9.]/g, '');
  // 确保只有一个小数点
  const parts = numericValue.split('.');
  if (parts.length > 2) {
    return;
  }
  // 限制小数点后两位
  if (parts.length === 2 && parts[1].length > 2) {
    return;
  }
  productPrices.value[productIndex] = numericValue;
  
  // 将商品价格分配给该商品的所有规格
  let specIndex = 0;
  for (let i = 0; i < productIndex; i++) {
    specIndex += products.value[i].specs.length;
  }
  
  const product = products.value[productIndex];
  for (let i = 0; i < product.specs.length; i++) {
    editPricePerKg.value[specIndex + i] = numericValue;
  }
};

// 保存编辑的价格
const savePrices = () => {
  // 验证输入
  const isValid = editPrices.value.every(price => {
    if (price === '—' || price === '') return true;
    const numPrice = parseFloat(price as string);
    return !isNaN(numPrice) && numPrice >= 0;
  }) && editPricePerKg.value.every(price => {
    if (price === '—' || price === '') return true;
    const numPrice = parseFloat(price as string);
    return !isNaN(numPrice) && numPrice >= 0;
  });
  
  if (isValid) {
    // 构造 ProPriceRequest 数据结构
    const proPriceRequests = [];
    let specIndex = 0;
    
    products.value.forEach(product => {
      product.specs.forEach(spec => {
        const price = editPrices.value[specIndex];
        const pricePerKg = editPricePerKg.value[specIndex];
        if (price !== '—' && price !== '' || pricePerKg !== '—' && pricePerKg !== '') {
          const priceValue = price !== '—' && price !== '' ? parseFloat(price as string) : 0;
          const pricePerKgValue = pricePerKg !== '—' && pricePerKg !== '' ? parseFloat(pricePerKg as string) : 0;
          proPriceRequests.push({
            productName: product.name,
            spec: specLabel(spec),
            total: 0, // 后端会计算
            pricePerKg: pricePerKgValue,
            pricePerItem: priceValue,
            totalAmount: 0 // 后端会计算
          });
        }
        specIndex++;
      });
    });
    
    // 更新价格数据
    pricePerItemRow.value = [...editPrices.value];
    pricePerKgRow.value = [...editPricePerKg.value];
    // 退出编辑模式
    exitEditMode();
    // 触发保存事件，传递构造的数据
    emit('save-prices', proPriceRequests);
  } else {
    // 显示错误提示
    alert('请输入有效的价格');
  }
};

// 处理价格输入变化
const handlePriceChange = (index: number, value: string) => {
  // 只允许输入数字和小数点
  const numericValue = value.replace(/[^0-9.]/g, '');
  // 确保只有一个小数点
  const parts = numericValue.split('.');
  if (parts.length > 2) {
    return;
  }
  // 限制小数点后两位
  if (parts.length === 2 && parts[1].length > 2) {
    return;
  }
  editPrices.value[index] = numericValue;
};

// 处理 pricePerKg 输入变化
const handlePricePerKgChange = (index: number, value: string) => {
  // 只允许输入数字和小数点
  const numericValue = value.replace(/[^0-9.]/g, '');
  // 确保只有一个小数点
  const parts = numericValue.split('.');
  if (parts.length > 2) {
    return;
  }
  // 限制小数点后两位
  if (parts.length === 2 && parts[1].length > 2) {
    return;
  }
  editPricePerKg.value[index] = numericValue;
};

// 开始编辑计量单位
const startEditUnit = () => {
  isEditingUnit.value = true;
  editUnit.value = unitOfMeasure.value;
};

// 保存计量单位
const saveUnit = () => {
  // 验证输入
  if (editUnit.value.trim()) {
    unitOfMeasure.value = editUnit.value.trim();
    isEditingUnit.value = false;
    // 触发保存事件
    emit('save-unit', unitOfMeasure.value);
  } else {
    alert('请输入有效的计量单位');
  }
};

// 取消编辑计量单位
const cancelEditUnit = () => {
  isEditingUnit.value = false;
  editUnit.value = unitOfMeasure.value;
};

// 清空所有输入框
const clearPrices = () => {
  // 清空商品价格数组
  productPrices.value = productPrices.value.map(() => '');
  // 清空规格价格数组
  editPrices.value = editPrices.value.map(() => '');
  editPricePerKg.value = editPricePerKg.value.map(() => '');
  // 触发清空事件
  emit('clear-prices');
};

// 监听测试数据变化

// 监听 testData 变化
watch(() => props.testData, (newData) => {
  if (newData) {
    handleGenerateTestData(newData);
  }
}, { deep: true });

// 初始加载时处理数据
if (props.testData) {
  handleGenerateTestData(props.testData);
}

// 检测表格数据是否为空
const checkDataStatus = () => {
  const hasData = tableData.value.length > 0 || products.value.length > 0;
  emit('data-status', hasData);
};

// 监听表格数据变化
watch([tableData, products], () => {
  checkDataStatus();
}, { deep: true });

// 初始化数据
onMounted(async () => {
  try {
    const data = await fetchCheckoutData();
    console.log('从后端获取的结账数据:', data);
    
    // 转换数据并更新表格
    const transformedData = transformData(data);
    tableData.value = transformedData;
    
    console.log('转换后的表格数据:', transformedData);
    
    // 检查数据状态
    checkDataStatus();
  } catch (error) {
    console.error('获取结账数据失败:', error);
    // 检查数据状态
    checkDataStatus();
  }
});
</script>

<style scoped>
.table-card {
  background-color: var(--color-surface);
  border: 1px solid var(--color-line);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-card);
  padding: 16px;
  margin: 0;
}

.table-wrapper {
  --group-header-h: 46px;
  --spec-header-h: 42px;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-line);
  overflow: auto;
  background-color: var(--color-surface);
  max-height: calc(100vh - 280px);
}

table {
  width: 100%;
  /* fixed + colgroup：第一列宽度由 clamp 锁定，剩余宽度由规格列平分，
     避免规格列少时自动布局把第一列撑大；实际最小宽度由 tableStyle 动态计算 */
  table-layout: fixed;
  /* separate + 0 间距：sticky 表头在 Chrome/WebView 下滚动时不会丢失背景与边框 */
  border-collapse: separate;
  border-spacing: 0;
  font-size: 0.9rem;
}

th {
  background: var(--color-subtle);
  color: var(--color-ink-2);
  font-weight: 600;
  font-size: 0.85rem;
  padding: 14px 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  text-align: center;
}

/* 固定表头：两行表头悬浮在表格滚动层内，纵向滚动时不随数据移动 */
thead th {
  position: sticky;
  z-index: 3;
  background-color: var(--color-subtle);
  box-shadow: inset 0 -1px 0 var(--color-line);
}

thead .product-group th {
  top: 0;
  height: var(--group-header-h);
  box-sizing: border-box;
  padding: 0 8px;
  background-color: var(--color-subtle);
  color: var(--color-ink-2);
  font-size: 0.85rem;
  font-weight: 600;
}

thead .spec-header th {
  top: var(--group-header-h);
  height: var(--spec-header-h);
  box-sizing: border-box;
  padding: 0 12px;
  background-color: var(--color-subtle);
  color: var(--color-ink-3);
  font-weight: 500;
  box-shadow: inset 0 -1px 0 var(--color-line-strong), 0 2px 6px rgba(38, 42, 46, 0.05);
}

/* 左上角「日期 / 产品」：横向 + 纵向双向固定，层级高于其它表头单元格 */
thead th.group-label {
  left: 0;
  z-index: 6;
  padding-left: 18px;
  text-align: left;
  font-weight: 700;
  color: var(--color-ink-2);
  background-color: var(--color-subtle);
  box-shadow: inset -1px 0 0 var(--color-line), inset 0 -1px 0 var(--color-line);
}

td {
  padding: 12px;
  text-align: center;
  vertical-align: middle;
  color: var(--color-ink);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.data-row:hover td {
  background-color: var(--color-hover);
}

.date-column,
.group-label {
  position: sticky;
  left: 0;
  background-color: var(--color-subtle);
  text-align: left;
  padding-left: 18px;
  font-weight: 600;
  color: var(--color-ink-2);
  font-size: 0.95rem;
  z-index: 1;
}

/* 数据行的日期列（非表头）保持表面白底，横向滚动时与卡片一致 */
.date-column {
  background-color: var(--color-surface);
}

.spec-cell {
  font-weight: 500;
  color: var(--color-ink-2);
}

.spec-value {
  font-weight: 600;
  color: var(--color-ink);
  font-size: 1rem;
}

.spec-unit {
  font-size: 0.75rem;
  color: var(--color-ink-3);
  margin-left: 2px;
  font-weight: 400;
}

.empty-cell {
  color: var(--color-ink-3);
  font-size: 0.9rem;
}

/* 汇总行（总计 / 单价） */
.summary-row {
  background: var(--color-subtle);
}

.summary-row td {
  background: var(--color-subtle);
  font-weight: 600;
  color: var(--color-ink-2);
}

.summary-label {
  font-weight: 700;
  color: var(--color-ink-2);
  text-align: left;
  padding-left: 18px;
}

/* 结论行（总金额合计）— 品牌浅底高亮 */
.conclusion-row {
  background: var(--color-brand-soft);
}

.conclusion-row td {
  background: var(--color-brand-soft);
  font-weight: 600;
  color: var(--color-brand-ink);
}

.conclusion-label {
  font-weight: 700;
  color: var(--color-brand-ink);
  text-align: left;
  padding-left: 18px;
}

/* 总金额行：跨所有规格列展示金额行合计 */
.grand-total-row td {
  padding-top: 14px;
  padding-bottom: 14px;
}

.grand-total-cell {
  text-align: center !important;
  border-top: 1px solid var(--color-line-strong);
}

.grand-total-value {
  font-size: 1.15rem;
  font-weight: 700;
  color: var(--color-brand-ink);
  letter-spacing: 0.02em;
}

.grand-total-unit {
  margin-left: 6px;
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--color-brand-ink);
  opacity: 0.75;
}

/* 无数据空状态：替代只有一列、会被 100% 宽度撑满的空白表格 */
.table-empty {
  min-height: 260px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  text-align: center;
}

.table-empty-icon {
  width: 36px;
  height: 36px;
  color: var(--color-ink-disabled);
  margin-bottom: 4px;
}

.table-empty-title {
  margin: 0;
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--color-ink-2);
}

.table-empty-hint {
  margin: 0;
  font-size: 0.8rem;
  color: var(--color-ink-3);
}

/* 响应式 */
@media (max-width: 768px) {
  .table-card {
    margin: 10px;
    padding: 16px 12px;
  }
}
</style>