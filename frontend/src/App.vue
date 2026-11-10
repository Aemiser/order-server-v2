<script setup lang="ts">
import { ref, computed, reactive, onMounted, onUnmounted, watch } from 'vue';
import { Download, RotateCw, SlidersHorizontal, Wallet, DollarSign, RefreshCw, X, Check, AlertTriangle, Loader2, KeyRound, Lock, ShieldCheck, PanelRightClose, PanelRightOpen, Eraser } from 'lucide-vue-next';
import { ElDatePicker } from 'element-plus';
import * as XLSX from 'xlsx';

import Sidebar from './components/Sidebar/Sidebar.vue';
import demo from './components/demo.vue';
import Header from './components/Header/Header.vue';
import FieldDialog from './components/Main/Dialog/Field.vue';
import ActionRuleModal from './components/Main/Dialog/ActionRuleModal.vue';
import ChangelogModal from './components/ChangelogModal.vue';
import LoginView from './components/Auth/LoginView.vue';

import OrderInputView from './components/Main/OrderInputView.vue';
import OrderDetailsView from './components/Main/OrderDetailsView.vue';
import AppSettingsView from './components/Main/AppSettingsView.vue';
import CheckoutView from './components/Main/Checkout/CheckoutView.vue';
import DashboardView from './components/Main/DashboardView.vue';
import SchemaStudioView from './components/Main/Schema/SchemaStudioView.vue';
import { OrderRecognition, GetpreviewData, DeletepreviewData, CreateOrder, GetDaydata, GetAuthState, Logout, ChangePassword, RestartApplication, WarmupDashboard } from '../wailsjs/go/main/App';
import { EventsOn, EventsOff } from '../wailsjs/runtime/runtime';
import { updateState, appVersion, restartPrompt, showRestartPrompt, dismissRestartPrompt, changelogModalOpen, closeChangelogModal } from './stores/update';
import { recognitionState, noteRecognitionProgress, resetRecognition } from './stores/recognition';
import type { Order, ReviewOrder } from './types';


// --- 登录态 ---
const authState = reactive({
  loggedIn: false,
  username: '',
  nickname: '',
  role: '',
  // guest = 后端「启动免登录」开关关闭时自动建立的内置访客会话
  guest: false,
});

// 登录态是否已从后端同步完成：未就绪时登录页和主界面都不挂载，
// 这样免登录模式下不会出现「闪一帧登录页再跳主页」。
const authReady = ref(false);

// 进入首页就异步预热经营看板数据：后端幂等唤醒，只做一次全量加载，
// 之后用户点开「经营看板」只读内存、不触发任何 SQL。
// 刻意不 await，登录态同步与首屏渲染不会被数据库操作阻塞。
const warmupDashboard = () => {
  WarmupDashboard().catch(() => {});
};

// 从后端进程同步登录态：界面重载、登录后都会调用。
// 免登录模式下后端直接返回访客会话（loggedIn=true），登录页根本不会挂载，
// 主界面及其所有业务请求正常渲染，不需要在前端伪造任何登录动作。
const refreshAuthState = async () => {
  try {
    const state = await GetAuthState();
    if (state && state.loggedIn) {
      warmupDashboard();
      // 界面重载后与 Go 侧预览区 / 人工审查区对齐（登录态就绪后才调得通后端）
      void syncPreviewFromBackend();
      authState.loggedIn = true;
      authState.username = state.username || '';
      authState.nickname = state.nickname || '';
      authState.role = state.role || '';
      authState.guest = !!state.guest;
    } else {
      authState.loggedIn = false;
      authState.username = '';
      authState.nickname = '';
      authState.role = '';
      authState.guest = false;
    }
  } catch {
    // 绑定不可用时保持未登录，避免误入主界面
  } finally {
    authReady.value = true;
  }
};

const showPasswordDialog = ref(false);
const pwdForm = reactive({ oldPassword: '', newPassword: '', confirmPassword: '' });
const pwdError = ref('');
const pwdSaving = ref(false);

// --- 侧栏显示模式：简洁（收起）/ 详细（展开），偏好持久化 ---
const readPanelPref = (key: string, fallback: boolean) => {
  try {
    const saved = localStorage.getItem(key);
    if (saved !== null) return saved === 'compact';
  } catch { /* 隐私模式下 localStorage 不可用，忽略 */ }
  return fallback;
};
const savePanelPref = (key: string, compact: boolean) => {
  try { localStorage.setItem(key, compact ? 'compact' : 'full'); } catch { /* 忽略 */ }
};

const sidebarCompact = ref(readPanelPref('ordermaster.sidebar.mode', typeof window !== 'undefined' && window.innerWidth < 1024));
const contextCompact = ref(readPanelPref('ordermaster.context.mode', false));

const toggleSidebarCompact = () => {
  sidebarCompact.value = !sidebarCompact.value;
  savePanelPref('ordermaster.sidebar.mode', sidebarCompact.value);
};
const toggleContextCompact = () => {
  contextCompact.value = !contextCompact.value;
  savePanelPref('ordermaster.context.mode', contextCompact.value);
};

// --- State ---
const activeTab = ref('input');
const inputText = ref('');
const orders = ref<Order[]>([]);
// 不确认订单：停在人工审查区，必须由用户点「加入预览区」才会进 orders
const reviewOrders = ref<ReviewOrder[]>([]);
// 人工审查区里的「订单操作」条目（删除 / 修改），供右侧概览提示。
const actionReviewCount = computed(() => reviewOrders.value.filter(i => i.kind === 'action').length);
const targetDate = ref('');
const reviewBusy = ref(false);
const isAnalyzing = ref(false);
const isSystemReady = ref(true);
const statusMessage = ref('系统已就绪 (后端模式)');
const showConfirmModal = ref(false);
const showFieldDialog = ref(false);
// 人工审核区旁边的「操作术语」快捷入口：把本轮没认出来的消息当参考带进弹窗。
const showActionRuleModal = ref(false);
const actionRulePrefill = ref('');
const actionRuleReferences = computed(() =>
  reviewOrders.value.map(i => (i.rawContent || '').trim()).filter(Boolean).slice(0, 30)
);
const openActionRules = () => {
  actionRulePrefill.value = '';
  showActionRuleModal.value = true;
};
const handleActionRuleSaved = () => {
  statusMessage.value = '订单操作术语已保存，下一轮「一键识别订单」立即生效';
  setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 4000);
};
const isEditMode = ref(false);
const checkoutResponse = ref<any>(null);
const isLoading = ref(false);
const proPriceRequests = ref<any[]>([]);
const hasCheckoutData = ref(true); // 用于跟踪结账表格是否有数据
const selectedDateRange = ref<{start: string, end: string}>({
  start: '2026-03-01',
  end: '2026-03-31'
});

// 时间选择器相关状态
const selectedDateTime = ref<Date>(new Date());
const dateError = ref('');

// 字段数据
const fieldData = ref<{
  fieldList: string[];
  pbMap: Record<string, string[]>
}>({
  fieldList: [],
  pbMap: {}
});

// 数据加载状态
const isLoadingFieldData = ref(false);
const fieldDataLoaded = ref(false);

// 设置事件监听（download 等运行时事件）
// 右下角提醒里的「立即重启」：先收起提醒，再让后端替换 exe 并重启
const restartFromToast = () => {
  dismissRestartPrompt();
  RestartApplication();
};

let unsubscribers: (() => void)[] = [];

onMounted(() => {
  // 版本更新：顶栏显示进度；下载完成时右下角弹一次轻量提醒，不强制、不阻塞
  const unsub1 = EventsOn('update_checking', () => {
    // 后台每 1 分钟轮询也会发此事件，非首次状态时不让顶栏反复闪「检查更新中」
    if (updateState.status !== 'idle') return;
    updateState.status = 'checking';
  });

  const unsub2 = EventsOn('update_available', (version: string, name: string) => {
    updateState.version = version;
    updateState.name = name || version;
    updateState.status = 'downloading';
  });

  const unsub3 = EventsOn('update_uptodate', (current: string) => {
    updateState.status = 'uptodate';
    updateState.currentVersion = current || '';
    // 后端更新检查会带上当前版本号，作为版本绑定的兜底来源
    if (!appVersion.value && current) appVersion.value = current;
  });

  const unsub4 = EventsOn('download_progress', (current: number, total: number, percentage: number, speed: number) => {
    updateState.status = 'downloading';
    updateState.progress = percentage;
    updateState.speed = speed;
  });

  const unsub5 = EventsOn('download_complete', (_filepath: string) => {
    updateState.status = 'ready';
    updateState.progress = 100;
    // 下载完成弹一次右下角轻量提醒；关闭后顶栏「已就绪」仍可随时重启
    showRestartPrompt(updateState.version);
  });

  const unsub6 = EventsOn('download_error', (err: string) => {
    updateState.status = 'error';
    updateState.errorMsg = err;
  });

  // 后端检测到会话失效时，强制回到登录页
  const unsubAuth = EventsOn('auth_required', () => {
    forceLogout();
  });

  // 一键识别进度：订阅放在常驻根组件，切 tab 不会销毁。
  // 只用 EventsOn 返回的取消函数，不用 EventsOff（按事件名会清掉所有监听，误伤别人）。
  const unsubRecog = EventsOn('wechat-order-progress', (p: any) => {
    noteRecognitionProgress(p);
  });

  // 界面重新加载后，从后端进程恢复登录状态（成功后会顺带对齐预览区数据）
  refreshAuthState();

  unsubscribers = [unsub1, unsub2, unsub3, unsub4, unsub5, unsub6, unsubAuth, unsubRecog];
});

onUnmounted(() => {
  unsubscribers.forEach(fn => fn());
  EventsOff('update_checking', 'update_available', 'update_uptodate', 'download_progress', 'download_complete', 'download_error', 'auth_required');
});

// --- Logic ---
const analyzeOrders = async (isAppend: boolean = false, modelId: number = 0) => {
  if (!inputText.value.trim()) return;

  isAnalyzing.value = true;
  statusMessage.value = '正在识别订单数据...';

  try {
    // 调用后端的OrderRecognition方法，参数：上下文，AI模型ID，预览类型
    const previewType = isAppend ? 1 : 0;
    console.log('调用OrderRecognition:', {
      context: inputText.value,
      modelId: modelId,
      previewType: previewType
    });
    const result = await OrderRecognition(inputText.value, modelId, previewType);
    console.log('识别结果:', result);
    // 转换后端返回的数据为Order类型
    const newOrders: Order[] = [];

    if (Array.isArray(result)) {
      orders.value = [];
      result.forEach((item: any, index: number) => {

        newOrders.push({
          id: item.PreviewID || item['VID'],
          name: item.Recipient || item['收件人'] || '未知姓名',
          phone: item.Phone || item['手机号'] || '未知电话',
          address: item.Address || item['收货地址'] || '未知地址',
          items: item.ProductSpecs || item['商品规格'] || '-',
          note: item.Remarks || item['备注'] || '-'
        });
      });
    }

    if (newOrders.length > 0) {
      orders.value = [...orders.value, ...newOrders];
      inputText.value = '';
      statusMessage.value = `成功解析 ${newOrders.length} 条订单`;
    } else {
      statusMessage.value = '解析失败：格式不匹配';
    }

    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  } catch (error) {
    console.error("Error calling backend:", error);
    statusMessage.value = '解析出错：后端服务异常';
  } finally {
    isAnalyzing.value = false;
  }
};

// 后端返回的预览行 → 前端表格行
const mapPreviewItem = (item: any, i: number): Order => ({
  id: String(item.VID ?? item.PreviewID ?? i + 1),
  name: item['收件人'] || item.Recipient || '-',
  phone: item['手机号'] || item.Phone || '-',
  address: item['收货地址'] || item.Address || '-',
  items: item['商品规格'] || item.ProductSpecs || '-',
  note: item['备注'] || item.Remarks || '-'
});

// 一键识别订单：确认订单进「数据预览」，不确认订单进「待人工审查」区，
// 全程不写数据库、不生成文件。
const handleAutoFetchResult = (result: any) => {
  if (!result?.success) {
    reviewOrders.value = [];
    statusMessage.value = '识别失败：' + (result?.message || '未知错误');
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 5000);
    return;
  }
  const list = Array.isArray(result.orders) ? result.orders : [];
  orders.value = list.map(mapPreviewItem);
  reviewOrders.value = Array.isArray(result.reviewOrders) ? result.reviewOrders : [];
  targetDate.value = result.targetDate || '';
  statusMessage.value = result.message || '识别完成，结果已填入数据预览区';
  setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 6000);
};

// 人工审查：确认无误后把该条（ids 为空 = 全部）加入数据预览区。
// 后端会把订单追加进 global.AddressList，所以必须用后端返回的最新列表刷新表格，
// 否则后续「确认数据无误」入库时会用到不一致的数据。
const appBridge = () => (window as any)['go']['main']['App'];

// ---------------- 一键识别：跨页面常驻状态 ----------------
// 结果不再靠子组件 emit 上抛（组件切页销毁后 Vue 3 会直接吞掉 emit），
// 统一由 recognition store 的 lastResult 驱动回填；这些 watch 注册在根组件，
// 用户切到任何页面都不会影响识别继续执行与结果落地。
const recognitionToastVisible = ref(false);
// 只有「识别在别的页面完成」时才需要切回订单页重新对齐，
// 避免把用户在预览表里手动作出的改动（移除行等）用后端数据覆盖回去。
const previewSyncPending = ref(false);

watch(() => recognitionState.lastResult, (result) => {
  if (!result) return;
  handleAutoFetchResult(result);
  if (activeTab.value !== 'input') {
    recognitionToastVisible.value = true;
    previewSyncPending.value = true;
    window.setTimeout(() => { recognitionToastVisible.value = false; }, 12000);
  }
});

watch(() => recognitionState.running, (n, prev) => {
  if (n > 0) {
    statusMessage.value = '订单识别中…（可切换页面，后台继续运行）';
  } else if (prev > 0 && recognitionState.error && !recognitionState.summary) {
    // 调用异常、没有 result 时补一次失败提示；有 result 的失败由 handleAutoFetchResult 处理
    statusMessage.value = '识别失败：' + recognitionState.error;
    window.setTimeout(() => { statusMessage.value = '系统已就绪 (后端模式)'; }, 5000);
  }
});

// 切回「订单输入」时与 Go 侧预览区 / 人工审查区对齐，覆盖界面重载、
// emit 丢失、其它页面把漏单加入预览区等所有前后端不一致场景。
// 识别进行中跳过，避免用中间态覆盖；跑完后由上面的 lastResult watch 回填。
const syncPreviewFromBackend = async () => {
  try {
    const [rows, reviews] = await Promise.all([
      appBridge()['GetpreviewData'](),
      appBridge()['GetWechatReviewOrders'](),
    ]);
    if (Array.isArray(rows)) orders.value = rows.map(mapPreviewItem);
    if (Array.isArray(reviews)) reviewOrders.value = reviews;
  } catch (e) {
    console.warn('恢复数据预览区失败', e);
  }
};

watch(activeTab, (tab) => {
  if (tab !== 'input' || recognitionState.running > 0 || !previewSyncPending.value) return;
  previewSyncPending.value = false;
  void syncPreviewFromBackend();
});

const handlePromoteReview = async (ids: string[]) => {
  if (reviewBusy.value) return;
  reviewBusy.value = true;
  try {
    const latest = await appBridge()['PromoteWechatReviewOrders'](ids);
    if (Array.isArray(latest)) {
      orders.value = latest.map(mapPreviewItem);
    }
    const left = await appBridge()['GetWechatReviewOrders']();
    reviewOrders.value = Array.isArray(left) ? left : [];
    statusMessage.value = '已把确认的订单加入数据预览区';
  } catch (e: any) {
    statusMessage.value = '加入预览区失败：' + (e?.message || String(e));
  } finally {
    reviewBusy.value = false;
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 4000);
  }
};

const handleDiscardReview = async (ids: string[]) => {
  if (reviewBusy.value) return;
  reviewBusy.value = true;
  try {
    const left = await appBridge()['DiscardWechatReviewOrders'](ids);
    reviewOrders.value = Array.isArray(left) ? left : [];
    // 订单操作项被忽略时后端会撤销操作、把原始订单放回数据预览区，
    // 必须重新拉一次预览表，否则后续「确认数据无误」会用到不一致的数据。
    const latestRows = await appBridge()['GetpreviewData']();
    if (Array.isArray(latestRows)) {
      orders.value = latestRows.map(mapPreviewItem);
    }
    statusMessage.value = '已忽略该条待审查数据（订单操作已撤销，原单已恢复）';
  } catch (e: any) {
    statusMessage.value = '忽略失败：' + (e?.message || String(e));
  } finally {
    reviewBusy.value = false;
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 4000);
  }
};

// 账单页人工审核区把确认的漏单追加进 global.AddressList 后，
// 必须同步刷新这里的数据预览表，否则切回「订单输入」页会看到旧列表。
const handleCheckoutPreviewChanged = (rows: any[]) => {
  if (!Array.isArray(rows)) return;
  orders.value = rows.map(mapPreviewItem);
  statusMessage.value = '已把审核确认的漏单加入数据预览区，请在「订单输入」页确认入库';
  setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 6000);
};

const clearInput = () => {
  inputText.value = '';
};

const removeOrder = (id: string) => {
  orders.value = orders.value.filter(o => o.id !== id);
};

const deleteOrder = async (id: string) => {
  try {
    // 调用后端的 DeletepreviewData 方法
    const result = await DeletepreviewData(parseInt(id));
    statusMessage.value = result;

    // 刷新数据
    await refreshData();

    // 3秒后恢复默认状态
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  } catch (error) {
    console.error('删除失败:', error);
    statusMessage.value = '删除出错：后端服务异常';
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  }
};

const exportToExcel = () => {
  // 定义字段映射关系
  const fieldMap = {
    name: '收件人',
    phone: '手机号',
    address: '收货地址',
    items: '规格信息',
    note: '备注'
  };

  // 转换数据：只保留需要的字段并重命名
  const exportData = orders.value.map(order => {
    const newRow = {};
    Object.keys(fieldMap).forEach(key => {
      if (order.hasOwnProperty(key)) {
        newRow[fieldMap[key]] = order[key];
      }
    });
    return newRow;
  });

  const worksheet = XLSX.utils.json_to_sheet(exportData);
  const workbook = XLSX.utils.book_new();
  XLSX.utils.book_append_sheet(workbook, worksheet, "Orders");
  XLSX.writeFile(workbook, `Orders_${new Date().toISOString().split('T')[0]}.xlsx`);
};

const refreshData = async () => {
  try {
    const result = await GetpreviewData();
    if (Array.isArray(result)) {
      const newOrders: Order[] = result.map((item: any) => ({
        id: item.VID,
        name: item['收件人'] || '未知姓名',
        phone: item['手机号'] || '未知电话',
        address: item['收货地址'] || '未知地址',
        items: item['商品规格'] || '-',
        note: item['备注'] || '-'
      }));
      orders.value = newOrders;
    } else {
      // 如果返回的数据不是数组，将 orders 设置为空数组
      orders.value = [];
    }
  } catch (error) {
    console.error('刷新数据失败:', error);
    // 发生错误时，也将 orders 设置为空数组
    orders.value = [];
  }
};

const totalOrders = computed(() => orders.value.length);
const estimatedSize = computed(() => (orders.value.length * 4.1).toFixed(1));
const contextTitle = computed(() => activeTab.value === 'input' ? '识别概览' : '结算概览');

// 计算选中的天数
const selectedDays = computed(() => {
  if (!selectedDateRange.value.start || !selectedDateRange.value.end) return 0;

  const startDate = new Date(selectedDateRange.value.start);
  const endDate = new Date(selectedDateRange.value.end);

  // 计算时间差（毫秒）
  const timeDiff = endDate.getTime() - startDate.getTime();

  // 转换为天数并取整
  const days = Math.ceil(timeDiff / (1000 * 3600 * 24)) + 1; // +1 因为包含开始和结束日期

  return days;
});

const createOrder = () => {
  // 重置时间选择器为当前时间
  selectedDateTime.value = new Date();
  // 清除日期错误
  dateError.value = '';
  // 显示确认模态弹窗
  showConfirmModal.value = true;
};

const handleConfirm = async () => {
  // 验证日期时间
  if (!selectedDateTime.value) {
    dateError.value = '请选择订单日期';
    return;
  }

  // 关闭模态弹窗
  showConfirmModal.value = false;

  try {
    console.log('调用CreateOrder:', { date: selectedDateTime.value });
    const result = await CreateOrder(selectedDateTime.value);
    statusMessage.value = result;
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  } catch (error) {
    console.error('创建订单失败:', error);
    statusMessage.value = '创建订单出错：后端服务异常';
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  }
};

const handleCancel = () => {
  // 关闭模态弹窗
  showConfirmModal.value = false;
  // 清除日期错误
  dateError.value = '';
};

// 处理字段配置
const handleConfig = () => {
  // 检查是否有字段数据
  if (fieldData.value.fieldList.length === 0 && Object.keys(fieldData.value.pbMap).length === 0) {
    // 如果没有字段数据，显示提示
    alert('请先执行查询以获取字段数据');
    return;
  }
  showFieldDialog.value = true;
};

// 处理保存字段配置
const handleSaveFieldConfig = (fieldMap: Record<string, string[]>) => {
  console.log('保存字段配置:', fieldMap);

  try {
    // 调用后端 SetProductBelong 函数
    window['go']['main']['App']['SetProductBelong'](fieldMap);
    console.log('字段配置保存成功');
    statusMessage.value = '字段配置保存成功';
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  } catch (error) {
    console.error('保存字段配置失败:', error);
    alert('保存字段配置失败，请稍后重试');
  }

  showFieldDialog.value = false;
};

// 处理进入编辑模式
const handleEnterEditMode = () => {
  isEditMode.value = true;

  // 初始化 proPriceRequests 状态
  if (checkoutResponse.value && checkoutResponse.value.ProSPecs) {
    const proPriceRequestsData = [];
    let specIndex = 0;

    checkoutResponse.value.ProSPecs.forEach((product: any) => {
      if (product.Specs) {
        product.Specs.forEach((spec: any) => {
          const pricePerItem = checkoutResponse.value.PricePerItemRow ? checkoutResponse.value.PricePerItemRow[specIndex] : 0;
          const pricePerKg = checkoutResponse.value.PricePerKgRow ? checkoutResponse.value.PricePerKgRow[specIndex] : 0;

          proPriceRequestsData.push({
            productName: product.Name || '',
            spec: typeof spec === 'object' ? (spec.name || spec.Name || JSON.stringify(spec)) : spec,
            total: 0, // 后端会计算
            pricePerKg: typeof pricePerKg === 'number' ? pricePerKg : 0,
            pricePerItem: typeof pricePerItem === 'number' ? pricePerItem : 0,
            totalAmount: 0 // 后端会计算
          });

          specIndex++;
        });
      }
    });

    proPriceRequests.value = proPriceRequestsData;
  } else {
    // 如果没有后端数据，使用默认商品数据初始化
    const defaultProducts = [
      { name: '香水柠檬', specs: ['5斤', '8斤', '10斤', '20斤'] },
      { name: '广香一级', specs: ['10斤', '20斤'] },
      { name: '广香二级', specs: ['7斤', '15斤'] },
      { name: '黄柠檬', specs: ['3斤', '6斤'] },
      { name: '金桔', specs: ['2斤', '4斤'] }
    ];

    const proPriceRequestsData = [];

    defaultProducts.forEach(product => {
      product.specs.forEach(spec => {
        proPriceRequestsData.push({
          productName: product.name,
          spec: spec,
          total: 0, // 后端会计算
          pricePerKg: 0,
          pricePerItem: 0,
          totalAmount: 0 // 后端会计算
        });
      });
    });

    proPriceRequests.value = proPriceRequestsData;
  }
};

// 处理退出编辑模式
const handleExitEditMode = () => {
  isEditMode.value = false;
};

// 处理保存价格
const handleSavePrices = async (data?: any[]) => {
  try {
    // 如果有数据传入，且数据是数组，存储到状态中
    if (data && Array.isArray(data)) {
      proPriceRequests.value = data;
    }

    // 验证 proPriceRequests 是否为空
    if (!proPriceRequests.value || proPriceRequests.value.length === 0) {
      statusMessage.value = '没有可保存的价格数据';
      setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
      return;
    }

    // 显示加载状态
    isLoading.value = true;
    statusMessage.value = '正在保存价格数据...';

    // 调用后端的 SetPriceData 方法
    const response = await window['go']['main']['App']['SetPriceData'](proPriceRequests.value);
    console.log('保存价格成功:', response);
    // 存储后端响应数据
    checkoutResponse.value = response;
    statusMessage.value = '价格保存成功';
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
    isEditMode.value = false;
  } catch (error) {
    console.error('保存价格失败:', error);
    statusMessage.value = '保存价格失败，请重试';
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  } finally {
    // 隐藏加载状态
    isLoading.value = false;
  }
};

// 处理保存计量单位
const handleSaveUnit = (unit: string) => {
  console.log('保存计量单位:', unit);
  // 这里可以添加保存计量单位的逻辑
  // 例如存储到本地存储或发送到后端
  statusMessage.value = '计量单位保存成功';
  setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
};

// 处理清空所有价格输入
const handleClearPrices = () => {
  // 清空价格请求数据
  proPriceRequests.value = [];
  statusMessage.value = '价格数据已清空';
  setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
};

// 处理结账表格数据状态变化
const handleCheckoutDataStatus = (hasData: boolean) => {
  hasCheckoutData.value = hasData;
};

// 处理结算账单
const handleCheckout = async () => {
  try {
    // 显示加载状态
    isLoading.value = true;
    statusMessage.value = '正在生成财务账单...';

    // 调用后端的 SaveCheackToExcel 方法
    const response = await window['go']['main']['App']['SaveCheackToExcel']();
    console.log('结算账单成功:', response);
    statusMessage.value = '财务账单生成成功';
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  } catch (error) {
    console.error('结算账单失败:', error);
    statusMessage.value = '生成财务账单失败，请重试';
    setTimeout(() => statusMessage.value = '系统已就绪 (后端模式)', 3000);
  } finally {
    // 隐藏加载状态
    isLoading.value = false;
  }
};

// 处理日期范围变化
const handleDateRangeChange = (dateRange: {start: string, end: string}) => {
  selectedDateRange.value = dateRange;
  console.log('日期范围已更新:', dateRange);
};

// 处理字段数据更新
const handleUpdateFieldData = (data: {
  fieldList: string[];
  pbMap: Record<string, string[]>;
}) => {
  fieldData.value = data;
  console.log('字段数据已更新:', data);

  // 检查PBMap是否有效
  fieldDataLoaded.value = !!data && !!data.pbMap && Object.keys(data.pbMap).length > 0;
  isLoadingFieldData.value = false;
};

// 处理字段数据加载状态
const handleFieldDataLoading = (isLoading: boolean) => {
  isLoadingFieldData.value = isLoading;
  console.log('字段数据加载状态:', isLoading);
};

// --- 登录 / 登出 / 修改密码 ---
const resetWorkspace = () => {
  authState.loggedIn = false;
  authState.username = '';
  authState.nickname = '';
  authState.role = '';
  authState.guest = false;
  activeTab.value = 'input';
  inputText.value = '';
  orders.value = [];
  reviewOrders.value = [];
  targetDate.value = '';
  checkoutResponse.value = null;
  showConfirmModal.value = false;
  showFieldDialog.value = false;
  showPasswordDialog.value = false;
  pwdForm.oldPassword = '';
  pwdForm.newPassword = '';
  pwdForm.confirmPassword = '';
  pwdError.value = '';
  // 登出一并清空识别状态，并让仍在跑的旧任务结果作废，避免串到下一个会话
  resetRecognition();
  recognitionToastVisible.value = false;
  previewSyncPending.value = false;
};

const forceLogout = () => {
  resetWorkspace();
};

const onLoginSuccess = (state: { username: string; nickname: string; role: string }) => {
  warmupDashboard();
  authState.loggedIn = true;
  authState.username = state.username;
  authState.nickname = state.nickname;
  authState.role = state.role;
  authState.guest = false;
};

const handleLogout = async () => {
  try {
    await Logout();
  } catch (e) {
    console.warn('退出登录请求失败', e);
  }
  resetWorkspace();
};

const openPasswordDialog = () => {
  pwdForm.oldPassword = '';
  pwdForm.newPassword = '';
  pwdForm.confirmPassword = '';
  pwdError.value = '';
  showPasswordDialog.value = true;
};

const handleChangePassword = async () => {
  if (pwdSaving.value) return;
  pwdError.value = '';

  if (!pwdForm.oldPassword) {
    pwdError.value = '请输入原密码';
    return;
  }
  if (pwdForm.newPassword.length < 8) {
    pwdError.value = '新密码长度不能少于 8 位';
    return;
  }
  if (pwdForm.newPassword !== pwdForm.confirmPassword) {
    pwdError.value = '两次输入的新密码不一致';
    return;
  }

  pwdSaving.value = true;
  try {
    const msg = await ChangePassword(pwdForm.oldPassword, pwdForm.newPassword);
    if (msg === '修改成功') {
      showPasswordDialog.value = false;
      pwdForm.oldPassword = '';
      pwdForm.newPassword = '';
      pwdForm.confirmPassword = '';
    } else {
      pwdError.value = msg || '修改失败，请重试';
    }
  } catch (e: any) {
    pwdError.value = '修改失败：' + (e?.message || String(e));
  } finally {
    pwdSaving.value = false;
  }
};

</script>

<template>
  <!-- 登录态未同步完成前不渲染任何业务界面，避免免登录模式下闪一帧登录页 -->
  <div v-if="!authReady" class="h-screen w-full flex items-center justify-center bg-app text-ink-3 text-sm">
    正在初始化工作区…
  </div>

  <!-- 未登录时只渲染登录页，主界面（含所有数据请求）完全不挂载 -->
  <LoginView v-else-if="!authState.loggedIn" @login-success="onLoginSuccess" />

  <div v-else class="flex h-screen w-full overflow-hidden bg-app text-ink font-sans">
    <!-- 一键识别全局状态：任何页面都可见，识别在后台继续跑，点击可回到订单输入页 -->
    <div
      v-if="recognitionState.running > 0 || (recognitionToastVisible && activeTab !== 'input')"
      class="fixed top-16 right-5 z-50 w-80 max-w-[calc(100vw-2.5rem)] rounded-lg border border-line bg-surface shadow-xl p-3.5"
    >
      <div class="flex items-start gap-2.5">
        <Loader2 v-if="recognitionState.running > 0" class="w-4 h-4 mt-0.5 shrink-0 animate-spin text-brand" />
        <Check v-else class="w-4 h-4 mt-0.5 shrink-0 text-success" />
        <div class="min-w-0 flex-1">
          <p class="text-sm font-medium text-ink">
            {{ recognitionState.running > 0
              ? `正在识别订单${recognitionState.running > 1 ? `（${recognitionState.running} 个任务）` : ''}`
              : '订单识别已完成' }}
          </p>
          <p class="text-xs text-ink-3 mt-1 break-words">
            {{ recognitionState.running > 0
              ? (recognitionState.progress || '正在准备…')
              : (recognitionState.error || recognitionState.summary?.message || '结果已填入数据预览区 / 人工审查区') }}
          </p>
          <p v-if="recognitionState.running > 0 && recognitionState.total > 0" class="text-xs text-ink-3 mt-1">
            进度 {{ recognitionState.done }} / {{ recognitionState.total }}
          </p>
        </div>
        <button
          v-if="recognitionState.running === 0"
          @click="recognitionToastVisible = false"
          class="text-ink-3 hover:text-ink text-base leading-none shrink-0"
          title="关闭"
        >×</button>
      </div>
      <div class="mt-2.5 flex items-center gap-2">
        <button
          @click="activeTab = 'input'"
          class="px-2.5 py-1 text-xs rounded border border-line text-ink-2 hover:bg-hover shrink-0"
        >查看识别结果</button>
        <span class="text-[11px] text-ink-3 truncate">
          {{ recognitionState.running > 0 ? '可继续浏览其它页面，识别在后台进行' : '结果已同步' }}
        </span>
      </div>
    </div>

    <Sidebar
      v-model:activeTab="activeTab"
      :isSystemReady="isSystemReady"
      :statusMessage="statusMessage"
      :collapsed="sidebarCompact"
      @toggle-collapse="toggleSidebarCompact"
    />

      <main class="flex-1 flex flex-col min-w-0 bg-app">
        <Header
          :userName="authState.nickname || authState.username"
          :userRole="authState.guest ? '免登录模式' : (authState.role === 'admin' ? '管理员' : '操作员')"
          :guest="authState.guest"
          @logout="handleLogout"
          @change-password="openPasswordDialog"
        />

        <div class="flex-1 flex flex-col lg:flex-row min-w-0 overflow-y-auto lg:overflow-hidden">
          <div class="flex-1 min-w-0 overflow-x-auto lg:overflow-y-auto p-4 lg:p-6 custom-scrollbar">
            <!-- Conditional View Rendering -->
        <OrderInputView
          v-if="activeTab === 'input'"
          v-model:inputText="inputText"
          :isAnalyzing="isAnalyzing"
          :orders="orders"
          :reviewOrders="reviewOrders"
          :targetDate="targetDate"
          :reviewBusy="reviewBusy"
          @clear="clearInput"
          @analyze="analyzeOrders"
          @removeOrder="removeOrder"
          @clearAllOrders="orders = []"
          @refreshData="refreshData"
          @delete="deleteOrder"
          @openSettings="activeTab = 'settings'"
          @promoteReview="handlePromoteReview"
          @discardReview="handleDiscardReview"
          @openActionRules="openActionRules"
        />

        <OrderDetailsView
          v-if="activeTab === 'details'"
          :activeTab="activeTab"
        />

        <CheckoutView
          v-if="activeTab === 'checkout'"
          :isEditMode="isEditMode"
          :checkoutResponse="checkoutResponse"
          @save-prices="handleSavePrices"
          @save-unit="handleSaveUnit"
          @enter-edit-mode="handleEnterEditMode"
          @exit-edit-mode="handleExitEditMode"
          @clear-prices="handleClearPrices"
          @data-status="handleCheckoutDataStatus"
          @date-range-change="handleDateRangeChange"
          @update-field-data="handleUpdateFieldData"
          @field-data-loading="handleFieldDataLoading"
          @preview-changed="handleCheckoutPreviewChanged"
        />

          <AppSettingsView v-if="activeTab === 'settings'" />

          <DashboardView v-if="activeTab === 'dashboard'" />

          <SchemaStudioView v-if="activeTab === 'schema'" />

         <!-- <demo />
          <Fidld /> -->
          </div>

          <!-- 右侧上下文面板：可收起 / 展开 -->
          <aside
            v-if="activeTab === 'input' || activeTab === 'checkout'"
            class="shrink-0 bg-surface border-line custom-scrollbar flex transition-all duration-200 ease-in-out"
            :class="contextCompact
              ? 'w-full h-12 flex-row items-center gap-2 px-3 border-t overflow-x-auto lg:h-full lg:w-12 lg:flex-col lg:justify-start lg:gap-3 lg:px-0 lg:py-3 lg:border-t-0 lg:border-l lg:overflow-visible'
              : 'w-full flex-col gap-4 p-4 border-t lg:w-[340px] lg:h-full lg:overflow-hidden lg:p-5 lg:border-t-0 lg:border-l'"
          >
            <!-- 简洁模式：操作以图标呈现，点击即可执行 -->
            <template v-if="contextCompact">
              <!-- 订单输入页：识别概览操作 -->
              <template v-if="activeTab === 'input'">
                <button
                  @click="createOrder"
                  title="确认数据无误"
                  class="flex items-center justify-center rounded-md w-9 h-9 text-ink-2 hover:bg-hover hover:text-ink transition-colors shrink-0"
                >
                  <Check class="w-5 h-5" />
                </button>
                <button
                  @click="exportToExcel"
                  :disabled="orders.length === 0"
                  title="导出 XLSX 表格"
                  class="flex items-center justify-center rounded-md w-9 h-9 text-ink-2 hover:bg-hover hover:text-ink disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                >
                  <Download class="w-5 h-5" />
                </button>
              </template>

              <!-- 账单结算页：非编辑模式 -->
              <template v-else-if="activeTab === 'checkout' && !isEditMode">
                <button
                  :disabled="!hasCheckoutData"
                  title="刷新数据"
                  class="flex items-center justify-center rounded-md w-9 h-9 text-ink-2 hover:bg-hover hover:text-ink disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                >
                  <RotateCw class="w-5 h-5" />
                </button>
                <button
                  @click="handleConfig"
                  :disabled="!fieldDataLoaded || isLoadingFieldData"
                  title="字段配置"
                  class="flex items-center justify-center rounded-md w-9 h-9 text-ink-2 hover:bg-hover hover:text-ink disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                >
                  <template v-if="isLoadingFieldData">
                    <span class="animate-spin"><svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path></svg></span>
                  </template>
                  <SlidersHorizontal v-else class="w-5 h-5" />
                </button>
                <button
                  @click="handleEnterEditMode"
                  :disabled="!hasCheckoutData"
                  title="编辑金额"
                  class="flex items-center justify-center rounded-md w-9 h-9 text-ink-2 hover:bg-hover hover:text-ink disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                >
                  <DollarSign class="w-5 h-5" />
                </button>
                <button
                  @click="handleCheckout"
                  :disabled="!hasCheckoutData"
                  title="财务结算"
                  class="flex items-center justify-center rounded-md w-9 h-9 bg-brand text-onbrand hover:bg-brand-hover disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                >
                  <Wallet class="w-5 h-5" />
                </button>
              </template>

              <!-- 账单结算页：编辑模式 -->
              <template v-else-if="activeTab === 'checkout' && isEditMode">
                <button
                  @click="handleClearPrices"
                  title="清空"
                  class="flex items-center justify-center rounded-md w-9 h-9 text-ink-2 hover:bg-hover hover:text-ink transition-colors shrink-0"
                >
                  <Eraser class="w-5 h-5" />
                </button>
                <button
                  @click="handleExitEditMode"
                  title="取消"
                  class="flex items-center justify-center rounded-md w-9 h-9 text-ink-2 hover:bg-hover hover:text-ink transition-colors shrink-0"
                >
                  <X class="w-5 h-5" />
                </button>
                <button
                  @click="() => handleSavePrices()"
                  :disabled="isLoading"
                  title="确定"
                  class="flex items-center justify-center rounded-md w-9 h-9 bg-brand text-onbrand hover:bg-brand-hover disabled:opacity-40 disabled:cursor-not-allowed transition-colors shrink-0"
                >
                  <template v-if="isLoading">
                    <span class="animate-spin"><svg class="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path></svg></span>
                  </template>
                  <Check v-else class="w-5 h-5" />
                </button>
              </template>

              <!-- 展开入口：始终在底部（桌面竖条） / 末尾（移动横条），与展开态收起按钮同高 -->
              <button
                @click="toggleContextCompact"
                :title="`展开${contextTitle}`"
                class="flex items-center justify-center rounded-md w-9 h-9 text-ink-3 hover:bg-hover hover:text-ink transition-colors shrink-0 ml-auto lg:ml-0 lg:mt-auto"
              >
                <PanelRightOpen class="w-5 h-5" />
              </button>
            </template>

            <template v-else>
              <!-- 面板内容（可滚动） -->
              <div class="flex flex-col gap-4 min-h-0 lg:flex-1 lg:overflow-y-auto custom-scrollbar">
                <!-- 面板头 -->
                <div class="flex items-center gap-2 shrink-0">
                  <div class="w-1 h-4 bg-brand rounded-full"></div>
                  <h2 class="text-sm font-semibold text-ink truncate">{{ contextTitle }}</h2>
                </div>

              <!-- 识别概览：仅订单输入页 -->
              <section v-if="activeTab === 'input'" class="surface-card p-5">
                <p class="text-ink-2 text-sm mb-4">共计识别 {{ totalOrders }} 条订单，预计文件大小 {{ estimatedSize }} KB</p>
              <p v-if="reviewOrders.length" class="text-xs text-warning-ink -mt-3 mb-4">
                另有 {{ reviewOrders.length }} 条不确认数据待人工审查，确认后才会计入此处
              </p>
              <p v-if="actionReviewCount" class="text-xs text-brand-ink -mt-3 mb-4">
                其中 {{ actionReviewCount }} 条为客户后续的订单操作（删除 / 修改），带原始与处理结果对照，人工采用或撤销后才会影响此处
              </p>
              <div class="flex flex-col gap-3">
                <button
                  @click="createOrder"
                  class="w-full px-4 py-2 rounded-md bg-brand hover:bg-brand-hover text-onbrand text-sm font-medium shadow-sm transition-colors"
                >
                  确认数据无误
                </button>
                <button
                  @click="exportToExcel"
                  :disabled="orders.length === 0"
                  class="w-full px-4 py-2 rounded-md bg-brand hover:bg-brand-hover disabled:opacity-50 disabled:cursor-not-allowed text-onbrand text-sm font-medium shadow-sm transition-colors flex items-center justify-center gap-2"
                >
                  <Download class="w-4 h-4" />
                  导出 XLSX 表格
                </button>
              </div>
            </section>

            <!-- 结算概览：仅账单结算页 -->
              <section v-if="activeTab === 'checkout'" class="surface-card p-5 flex flex-col gap-4">
                <div class="flex items-center gap-2 text-sm text-ink-2">
                <span class="w-2 h-2 rounded-full bg-brand"></span>
                <span>已选定 {{ selectedDays }} 天数据</span>
              </div>

              <!-- 非编辑模式 -->
              <div v-if="!isEditMode" class="flex flex-col gap-3">
                <button
                  :disabled="!hasCheckoutData"
                  class="w-full flex items-center justify-center gap-2 px-4 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
                >
                  <RotateCw class="w-4 h-4" />
                  刷新数据
                </button>
                <button
                  @click="handleConfig"
                  :disabled="!fieldDataLoaded || isLoadingFieldData"
                  class="w-full flex items-center justify-center gap-2 px-4 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
                >
                  <template v-if="isLoadingFieldData">
                    <div class="w-4 h-4 border-2 border-ink-3 border-t-brand rounded-full animate-spin"></div>
                  </template>
                  <SlidersHorizontal v-else class="w-4 h-4" />
                  {{ isLoadingFieldData ? '加载中...' : '字段配置' }}
                </button>
                <button
                  @click="handleEnterEditMode"
                  :disabled="!hasCheckoutData"
                  class="w-full flex items-center justify-center gap-2 px-4 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover disabled:opacity-50 disabled:cursor-not-allowed text-sm font-medium transition-colors"
                >
                  <DollarSign class="w-4 h-4" />
                  编辑金额
                </button>
                <button
                  @click="handleCheckout"
                  :disabled="!hasCheckoutData"
                  class="w-full flex items-center justify-center gap-2 px-4 py-2 rounded-md bg-brand hover:bg-brand-hover disabled:opacity-50 disabled:cursor-not-allowed text-onbrand text-sm font-medium shadow-sm transition-colors"
                >
                  <Wallet class="w-4 h-4" />
                  财务结算
                </button>
              </div>

              <!-- 编辑模式 -->
              <div v-else class="flex flex-col gap-3">
                <button
                  @click="handleClearPrices"
                  class="w-full px-4 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover text-sm font-medium transition-colors"
                >
                  清空
                </button>
                <button
                  @click="handleExitEditMode"
                  class="w-full px-4 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover text-sm font-medium transition-colors"
                >
                  取消
                </button>
                <button
                  @click="() => handleSavePrices()"
                  :disabled="isLoading"
                  class="w-full flex items-center justify-center gap-2 px-4 py-2 rounded-md bg-brand hover:bg-brand-hover disabled:opacity-50 disabled:cursor-not-allowed text-onbrand text-sm font-medium shadow-sm transition-colors"
                >
                  <span v-if="isLoading" class="animate-spin">
                    <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
                      <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"></path>
                    </svg>
                  </span>
                  {{ isLoading ? '保存中...' : '确定' }}
                </button>
              </div>
              </section>
              </div>

              <!-- 收起按钮：固定在底部，与左栏一致 -->
              <button
                @click="toggleContextCompact"
                :title="`收起${contextTitle}`"
                class="flex items-center justify-center gap-1.5 rounded-md w-full px-2 py-2 mt-4 text-xs text-ink-3 hover:text-ink hover:bg-hover transition-colors shrink-0 lg:w-9 lg:h-9 lg:px-0 lg:py-0 lg:mt-3"
              >
                <PanelRightClose class="w-4 h-4" />
                <span class="lg:hidden">收起</span>
              </button>
            </template>
          </aside>
        </div>

        <!-- Confirmation Modal -->
      <div v-if="showConfirmModal" class="fixed inset-0 bg-overlay flex items-center justify-center z-50 transition-opacity duration-300">
        <div class="bg-surface border border-line rounded-modal p-8 max-w-md w-full mx-4">
          <h3 class="text-xl font-bold text-ink mb-4">确认操作</h3>
          <p class="text-ink-2 mb-6">请确保今日的订单绝对正确，不然会影响结账！！！</p>

          <!-- 时间选择器 -->
          <div class="mb-6">
            <label class="block text-sm font-medium text-ink-2 mb-2">选择订单日期</label>
            <ElDatePicker
              v-model="selectedDateTime"
              type="datetime"
              placeholder="选择日期和时间"
              class="w-full"
              :popper-class="'date-picker-popper'"
              :clearable="false"
              size="large"
              :disabled-date="(time) => time.getTime() > new Date().getTime()"
            />
            <p v-if="dateError" class="text-xs text-danger mt-2">{{ dateError }}</p>
          </div>

          <div class="flex justify-end gap-4">
            <button @click="handleCancel" class="px-5 py-2 rounded-md border border-line-strong text-ink-2 hover:bg-hover font-medium transition-colors">
              取消
            </button>
            <button @click="handleConfirm" class="px-5 py-2 rounded-md bg-brand hover:bg-brand-hover text-onbrand font-medium transition-colors">
              确定
            </button>
          </div>
        </div>
      </div>

      <!-- Field Configuration Dialog -->
      <FieldDialog
        v-if="showFieldDialog"
        :show="showFieldDialog"
        :fieldList="fieldData.fieldList"
        :pbMap="fieldData.pbMap"
        @close="showFieldDialog = false"
        @save="handleSaveFieldConfig"
      />

      <!-- 订单操作术语配置（人工审核区快捷入口，共用配置页的同一套弹窗与绑定） -->
      <ActionRuleModal
        v-if="showActionRuleModal"
        :show="showActionRuleModal"
        :prefill="actionRulePrefill"
        :messages="actionRuleReferences"
        @close="showActionRuleModal = false"
        @saved="handleActionRuleSaved"
      />

      <!-- 版本更新历史 -->
      <ChangelogModal
        v-if="changelogModalOpen"
        @close="closeChangelogModal"
      />

      <!-- 修改密码 -->
      <div v-if="showPasswordDialog" class="fixed inset-0 bg-overlay flex items-center justify-center z-50">
        <div class="bg-surface border border-line rounded-lg p-8 max-w-md w-full mx-4">
          <div class="flex items-center gap-3 mb-6">
            <div class="w-10 h-10 rounded-full bg-brand-soft flex items-center justify-center">
              <KeyRound class="w-5 h-5 text-brand" />
            </div>
            <div>
              <h3 class="text-lg font-bold text-ink">修改密码</h3>
              <p class="text-xs text-ink-3 mt-0.5">当前账号：{{ authState.username }}</p>
            </div>
          </div>

          <div class="space-y-4">
            <div>
              <label class="block text-xs font-medium text-ink-2 mb-2">原密码</label>
              <div class="relative">
                <Lock class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-ink-3" />
                <input v-model="pwdForm.oldPassword" type="password" placeholder="请输入原密码"
                       class="w-full h-10 pl-9 pr-3 rounded-md bg-subtle border border-line text-sm text-ink placeholder:text-ink-3 outline-none focus:border-blue-500 transition-all" />
              </div>
            </div>
            <div>
              <label class="block text-xs font-medium text-ink-2 mb-2">新密码（至少 8 位）</label>
              <div class="relative">
                <Lock class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-ink-3" />
                <input v-model="pwdForm.newPassword" type="password" placeholder="请输入新密码"
                       class="w-full h-10 pl-9 pr-3 rounded-md bg-subtle border border-line text-sm text-ink placeholder:text-ink-3 outline-none focus:border-blue-500 transition-all" />
              </div>
            </div>
            <div>
              <label class="block text-xs font-medium text-ink-2 mb-2">确认新密码</label>
              <div class="relative">
                <Lock class="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-ink-3" />
                <input v-model="pwdForm.confirmPassword" type="password" placeholder="请再次输入新密码"
                       class="w-full h-10 pl-9 pr-3 rounded-md bg-subtle border border-line text-sm text-ink placeholder:text-ink-3 outline-none focus:border-blue-500 transition-all"
                       @keyup.enter="handleChangePassword" />
              </div>
            </div>
          </div>

          <div v-if="pwdError" class="mt-4 flex items-start gap-2 rounded-md bg-danger-soft border border-danger-border px-3 py-2.5">
            <AlertTriangle class="w-4 h-4 text-danger shrink-0 mt-0.5" />
            <span class="text-xs text-danger-ink">{{ pwdError }}</span>
          </div>

          <p class="mt-4 flex items-center gap-2 text-xs text-ink-3">
            <ShieldCheck class="w-3.5 h-3.5" />
            密码以 bcrypt 哈希存储，数据库中不保存明文
          </p>

          <div class="flex justify-end gap-3 mt-7">
            <button @click="showPasswordDialog = false"
                    class="px-6 py-2 rounded-lg bg-subtle hover:bg-hover text-ink-2 hover:text-ink text-sm font-medium transition-all">
              取消
            </button>
            <button @click="handleChangePassword" :disabled="pwdSaving"
                    class="px-7 py-2 rounded-lg bg-brand hover:bg-brand-hover disabled:opacity-50 text-onbrand text-sm font-bold flex items-center gap-2 transition-all">
              <Loader2 v-if="pwdSaving" class="w-4 h-4 animate-spin" />
              {{ pwdSaving ? '保存中...' : '确认修改' }}
            </button>
          </div>
        </div>
      </div>

    </main>
  </div>

  <!-- 更新下载完成：右下角小型提醒。放在三分支之外，登录页也能看到；不阻塞、不强制 -->
  <transition name="update-toast">
    <div v-if="restartPrompt.visible" class="update-toast" role="status" aria-live="polite">
      <div class="update-toast-top">
        <span class="update-toast-dot"></span>
        <span class="update-toast-title">新版本已下载完成</span>
        <button class="update-toast-x" @click="dismissRestartPrompt" aria-label="关闭提醒">
          <X class="w-3.5 h-3.5" />
        </button>
      </div>
      <p class="update-toast-text">
        版本 <b>{{ restartPrompt.version || '新版本' }}</b> 已就绪，重启后生效，当前使用不受影响。
      </p>
      <div class="update-toast-actions">
        <button class="update-toast-btn-later" @click="dismissRestartPrompt">稍后</button>
        <button class="update-toast-btn-restart" @click="restartFromToast">
          <RefreshCw class="w-3.5 h-3.5" />
          立即重启
        </button>
      </div>
    </div>
  </transition>
</template>

<style>
/* Element Plus 浅色主题已在 src/index.css 全局处理，此处无需覆写 */

/* 更新下载完成：右下角轻量提醒（非模态，不遮挡操作） */
.update-toast {
  position: fixed;
  right: 24px;
  bottom: 24px;
  z-index: 120;
  width: 320px;
  box-sizing: border-box;
  padding: 14px 16px 12px;
  background: var(--color-surface);
  border: 1px solid var(--color-line);
  border-left: 3px solid var(--color-success);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-pop);
  color: var(--color-ink);
}

.update-toast-top { display: flex; align-items: center; gap: 8px; }
.update-toast-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--color-success); flex-shrink: 0; }
.update-toast-title { flex: 1; font-size: 13px; font-weight: 600; color: var(--color-ink); }
.update-toast-x {
  display: flex; align-items: center; justify-content: center;
  width: 22px; height: 22px; padding: 0;
  border: 0; border-radius: 4px; background: transparent;
  color: var(--color-ink-3); cursor: pointer;
}
.update-toast-x:hover { background: var(--color-hover); color: var(--color-ink); }
.update-toast-text { margin: 8px 0 12px; font-size: 12px; line-height: 1.6; color: var(--color-ink-2); }
.update-toast-text b { font-weight: 600; color: var(--color-ink); }
.update-toast-actions { display: flex; justify-content: flex-end; gap: 8px; }

.update-toast-btn-later,
.update-toast-btn-restart {
  display: inline-flex; align-items: center; gap: 6px;
  height: 30px; padding: 0 12px; border-radius: 6px;
  font-size: 12px; font-weight: 500; cursor: pointer;
}
.update-toast-btn-later {
  background: var(--color-surface);
  border: 1px solid var(--color-line);
  color: var(--color-ink-2);
}
.update-toast-btn-later:hover { background: var(--color-hover); }
.update-toast-btn-restart {
  background: var(--color-brand);
  border: 1px solid var(--color-brand);
  color: var(--color-onbrand);
}
.update-toast-btn-restart:hover { background: var(--color-brand-hover); }

.update-toast-enter-active,
.update-toast-leave-active { transition: opacity .22s ease, transform .22s ease; }
.update-toast-enter-from,
.update-toast-leave-to { opacity: 0; transform: translateY(10px); }
</style>
