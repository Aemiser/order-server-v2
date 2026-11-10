<script setup lang="ts">
import { ref, reactive, computed } from 'vue';
import { Edit3, Trash2, Check, X } from 'lucide-vue-next';
import { SetpreviewData } from '../../../wailsjs/go/main/App';
import type { Order } from '../../types';

const props = defineProps<{
  orders: Order[];
}>();

const emit = defineEmits<{
  (e: 'remove', id: string): void;
  (e: 'clearAll'): void;
  (e: 'edit', order: Order): void;
  (e: 'refreshData'): void;
  (e: 'delete', id: string): void;
}>();

// 编辑状态管理
const currentEditingId = ref<string | null>(null);
const editedOrder = reactive<Partial<Order>>({});
const validationErrors = reactive<Record<string, string>>({});

// 开始编辑
const startEditing = (order: Order) => {
  currentEditingId.value = order.id;
  // 复制订单数据到编辑对象
  Object.assign(editedOrder, order);
  // 重置验证错误
  Object.keys(validationErrors).forEach(key => delete validationErrors[key]);
};

// 保存编辑
const saveEdit = async () => {
  // 验证输入
  validateForm();
  
  if (Object.keys(validationErrors).length === 0 && currentEditingId.value) {
    try {
      // 转换为后端需要的 ExceladdressInfo 类型
      const excelAddressInfo = {
        VID: parseInt(editedOrder.id || '0'),
        "收件人": editedOrder.name || '',
        "手机号": editedOrder.phone || '',
        "收货地址": editedOrder.address || '',
        "商品规格": editedOrder.items || '',
        "备注": editedOrder.note || ''
      };
      
      // Wails 生成的 ExceladdressInfo 带 createdAt / convertValues 等生成器附加成员，
      // Go 侧 JSON 反序列化只需下列数据字段；断言在编译期擦除，请求体不变。
      await SetpreviewData(excelAddressInfo as unknown as Parameters<typeof SetpreviewData>[0]);
      emit('edit', editedOrder as Order);
      emit('refreshData');
      currentEditingId.value = null;
    } catch (error) {
      console.error('保存失败:', error);
      validationErrors.general = '保存失败，请重试';
    }
  }
};

// 取消编辑
const cancelEdit = () => {
  currentEditingId.value = null;
  Object.keys(editedOrder).forEach(key => delete editedOrder[key]);
  Object.keys(validationErrors).forEach(key => delete validationErrors[key]);
};

// 验证表单
const validateForm = () => {
  Object.keys(validationErrors).forEach(key => delete validationErrors[key]);
  
  if (!editedOrder.name?.trim()) {
    validationErrors.name = '客户姓名不能为空';
  }
  
  if (!editedOrder.phone?.trim()) {
    validationErrors.phone = '联系电话不能为空';
  } else if (!/^1[3-9]\d{9}$/.test(editedOrder.phone)) {
    validationErrors.phone = '请输入有效的手机号码';
  }
  
  if (!editedOrder.address?.trim()) {
    validationErrors.address = '收货地址不能为空';
  }
  
  if (!editedOrder.items?.trim()) {
    validationErrors.items = '商品项不能为空';
  }
};

// 计算属性：检查是否正在编辑
const isEditing = (orderId: string) => {
  return currentEditingId.value === orderId;
};

// 计算属性：过滤掉general错误的验证错误
const filteredValidationErrors = computed(() => {
  const errors = { ...validationErrors };
  delete errors.general;
  return errors;
});

// 分页相关状态
const currentPage = ref(1); // 当前页码
const pageSize = ref(10); // 每页显示条数
const pageSizeOptions = [5, 10, 20, 25]; // 每页显示条数选项

// 计算总页数
const totalPages = computed(() => {
  return Math.ceil(props.orders.length / pageSize.value);
});

// 计算当前页显示的订单
const paginatedOrders = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value;
  const end = start + pageSize.value;
  return props.orders.slice(start, end);
});

// 切换页码
const changePage = (page: number) => {
  if (page < 1 || page > totalPages.value) return;
  currentPage.value = page;
};

// 改变每页显示条数
const changePageSize = (size: number) => {
  pageSize.value = size;
  currentPage.value = 1; // 重置到第一页
};

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
</script>

<template>
  <div class="space-y-4">
    <!-- 验证错误提示 -->
    <div v-if="Object.keys(validationErrors).length > 0" class="rounded-lg bg-danger-soft border border-danger-border p-4">
      <div class="flex items-start gap-3">
        <div class="text-danger">
          <svg xmlns="http://www.w3.org/2000/svg" class="h-5 w-5" fill="none" viewBox="0 0 24 24" stroke="currentColor">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-.77-1.964-.77-2.732 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
          </svg>
        </div>
        <div class="flex-1">
          <h3 class="text-danger font-medium mb-1">验证错误</h3>
          <ul class="text-sm text-danger-ink space-y-1">
            <li v-for="[key, error] in Object.entries(filteredValidationErrors)" :key="key">
              {{ error }}
            </li>
            <li v-if="validationErrors.general">
              {{ validationErrors.general }}
            </li>
          </ul>
        </div>
      </div>
    </div>
    
    <div class="rounded-lg border border-line bg-surface overflow-hidden shadow-card">
      <div class="overflow-x-auto">
        <table class="w-full text-left text-sm border-collapse">
          <thead class="bg-subtle text-ink-2 font-medium">
            <tr>
              <th class="px-6 py-4">编号</th>
              <th class="px-6 py-4">收件人</th>
              <th class="px-6 py-4">电话</th>
              <th class="px-6 py-4">地址</th>
              <th class="px-6 py-4">规格</th>
              <th class="px-6 py-4">备注</th>
              <th class="px-6 py-4 w-32">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-line">
            <tr v-for="(order, index) in paginatedOrders" :key="order.id" :class="[index % 2 === 1 ? 'bg-subtle/50' : '', isEditing(order.id) ? 'bg-brand-soft' : '']">
              <!-- 编号 -->
              <td class="px-6 py-4 font-mono text-ink-3">{{ order.id }}</td>
              
              <!-- 客户姓名 -->
              <td class="px-6 py-4">
                <template v-if="isEditing(order.id)">
                  <input 
                    v-model="editedOrder.name" 
                    type="text" 
                    class="w-full bg-surface border border-line-strong rounded px-3 py-2 text-ink focus:outline-none focus:border-brand transition-colors"
                    placeholder="收件人"
                  />
                  <span v-if="validationErrors.name" class="text-danger text-xs mt-1 block">{{ validationErrors.name }}</span>
                </template>
                <template v-else>
                  {{ order.name }}
                </template>
              </td>
              
              <!-- 联系电话 -->
              <td class="px-6 py-4">
                <template v-if="isEditing(order.id)">
                  <input 
                    v-model="editedOrder.phone" 
                    type="text" 
                    class="w-full bg-surface border border-line-strong rounded px-3 py-2 text-ink focus:outline-none focus:border-brand transition-colors font-mono"
                    placeholder="电话"
                  />
                  <span v-if="validationErrors.phone" class="text-danger text-xs mt-1 block">{{ validationErrors.phone }}</span>
                </template>
                <template v-else>
                  <span class="font-mono text-ink-2">{{ order.phone }}</span>
                </template>
              </td>
              
              <!-- 收货地址 -->
              <td class="px-6 py-4">
                <template v-if="isEditing(order.id)">
                  <input 
                    v-model="editedOrder.address" 
                    type="text" 
                    class="w-full bg-surface border border-line-strong rounded px-3 py-2 text-ink focus:outline-none focus:border-brand transition-colors"
                    placeholder="地址"
                  />
                  <span v-if="validationErrors.address" class="text-danger text-xs mt-1 block">{{ validationErrors.address }}</span>
                </template>
                <template v-else>
                  <span class="text-ink-2 max-w-xs truncate">{{ order.address }}</span>
                </template>
              </td>
              
              <!-- 商品项 -->
              <td class="px-6 py-4">
                <template v-if="isEditing(order.id)">
                  <input 
                    v-model="editedOrder.items" 
                    type="text" 
                    class="w-full bg-surface border border-line-strong rounded px-3 py-2 text-ink focus:outline-none focus:border-brand transition-colors"
                    placeholder="规格"
                  />
                  <span v-if="validationErrors.items" class="text-danger text-xs mt-1 block">{{ validationErrors.items }}</span>
                </template>
                <template v-else>
                  {{ order.items }}
                </template>
              </td>
              
              <!-- 备注 -->
              <td class="px-6 py-4">
                <template v-if="isEditing(order.id)">
                  <input 
                    v-model="editedOrder.note" 
                    type="text" 
                    class="w-full bg-surface border border-line-strong rounded px-3 py-2 text-ink focus:outline-none focus:border-brand transition-colors"
                    placeholder="备注"
                  />
                </template>
                <template v-else>
                  <span class="text-ink-3">{{ order.note || '-' }}</span>
                </template>
              </td>
              
              <!-- 操作 -->
              <td class="px-6 py-4">
                <template v-if="isEditing(order.id)">
                  <div class="flex gap-2">
                    <button 
                      @click="saveEdit"
                      class="p-1.5 rounded border border-line text-ink-2 hover:bg-hover hover:text-success transition-colors"
                      title="保存"
                    >
                      <Check class="w-4 h-4" />
                    </button>
                    <button 
                      @click="cancelEdit"
                      class="p-1.5 rounded border border-line text-ink-2 hover:bg-hover hover:text-ink transition-colors"
                      title="取消"
                    >
                      <X class="w-4 h-4" />
                    </button>
                  </div>
                </template>
                <template v-else>
                  <div class="flex gap-2">
                    <button 
                      @click="startEditing(order)"
                      class="p-1.5 rounded border border-line text-ink-2 hover:bg-hover hover:text-brand transition-colors"
                      title="编辑"
                    >
                      <Edit3 class="w-4 h-4" />
                    </button>
                    <button 
                      @click="$emit('delete', order.id)"
                      class="p-1.5 rounded border border-line text-ink-2 hover:bg-hover hover:text-danger transition-colors"
                      title="删除"
                    >
                      <Trash2 class="w-4 h-4" />
                    </button>
                  </div>
                </template>
              </td>
            </tr>
            <tr v-if="paginatedOrders.length === 0">
              <td colspan="7" class="px-6 py-12 text-center text-ink-3">
                暂无识别数据，请在上方输入订单文本并点击识别
              </td>
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
            显示 {{ (currentPage - 1) * pageSize + 1 }} 到 {{ Math.min(currentPage * pageSize, orders.length) }} 条，共 {{ orders.length }} 条记录
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
            <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
            </svg>
          </button>
          
          <!-- 页码按钮 -->
          <template v-if="totalPages <= 5">
            <button 
              v-for="page in totalPages" 
              :key="page"
              @click="changePage(page)"
              class="w-8 h-8 flex items-center justify-center rounded-md text-xs font-bold transition-colors"
              :class="currentPage === page ? 'bg-brand text-onbrand' : 'border border-line text-ink-2 hover:bg-hover'"
            >
              {{ page }}
            </button>
          </template>
          <template v-else>
            <!-- 第一页 -->
            <button 
              @click="changePage(1)"
              class="w-8 h-8 flex items-center justify-center rounded-md text-xs font-bold transition-colors"
              :class="currentPage === 1 ? 'bg-brand text-onbrand' : 'border border-line text-ink-2 hover:bg-hover'"
            >
              1
            </button>
            
            <!-- 省略号 -->
            <span v-if="currentPage > 3" class="text-ink-3 text-xs mx-1 px-1">...</span>
            
            <!-- 当前页附近的页码：v-if 必须落在 v-for 内层，
                 Vue 3 中同元素上 v-if 先于 v-for 求值，取不到 page -->
            <template v-for="page in Math.max(2, currentPage - 1)" :key="page">
              <button
                v-if="page < currentPage && page > 1"
                @click="changePage(page)"
                class="w-8 h-8 flex items-center justify-center rounded-md bg-surface border border-line text-ink-2 hover:bg-hover text-xs font-bold transition-colors"
              >
                {{ page }}
              </button>
            </template>
            
            <!-- 当前页 -->
            <button 
              class="w-8 h-8 flex items-center justify-center rounded-md bg-brand text-onbrand text-xs font-bold"
            >
              {{ currentPage }}
            </button>
            
            <!-- 当前页附近的页码：同上，v-if 放进 v-for 内层 -->
            <template v-for="page in Math.min(totalPages - 1, currentPage + 1)" :key="page">
              <button
                v-if="page > currentPage && page < totalPages"
                @click="changePage(page)"
                class="w-8 h-8 flex items-center justify-center rounded-md bg-surface border border-line text-ink-2 hover:bg-hover text-xs font-bold transition-colors"
              >
                {{ page }}
              </button>
            </template>
            
            <!-- 省略号 -->
            <span v-if="currentPage < totalPages - 2" class="text-ink-3 text-xs mx-1 px-1">...</span>
            
            <!-- 最后一页 -->
            <button 
              @click="changePage(totalPages)"
              class="w-8 h-8 flex items-center justify-center rounded-md text-xs font-bold transition-colors"
              :class="currentPage === totalPages ? 'bg-brand text-onbrand' : 'border border-line text-ink-2 hover:bg-hover'"
            >
              {{ totalPages }}
            </button>
          </template>
          
          <!-- 下一页按钮 -->
          <button 
            @click="nextPage"
            :disabled="currentPage === totalPages"
            class="w-8 h-8 flex items-center justify-center rounded-md border border-line text-ink-2 hover:bg-hover transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
          >
            <svg xmlns="http://www.w3.org/2000/svg" class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
            </svg>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
