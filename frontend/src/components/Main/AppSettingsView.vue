<script setup lang="ts">
import {
  MessageSquareMore, FolderOpen, Users, CalendarDays, Cpu, Save,
  RotateCcw, FlaskConical, Info, CheckCircle2, AlertCircle, Loader2,
  KeyRound, Trash2, ShieldCheck, Database, RefreshCw, Tags, PencilLine, Plus
} from 'lucide-vue-next';
import { ref, onMounted, onUnmounted } from 'vue';
import { EventsOn, EventsOff } from '../../../wailsjs/runtime/runtime';
import ActionRuleModal from './Dialog/ActionRuleModal.vue';
import type { OrderActionRule } from '../../types';
import {
  listActionRules, saveActionRule, deleteActionRule,
  actionLabel, fieldLabel, valueRuleLabel, scopeLabel,
} from '../../utils/action-rules';

interface WechatOrderConfig {
  group: string;
  senders: string;
  days: number;
  dataDir: string;
  workDir: string;
  dataKey: string;
  platform: string;
  version: number;
  autoKey: boolean;
  keyProviderPath: string;
  providerTimeout: number;
  autoKeyOnFailure: boolean;
  wcdbLibPath: string;
  sessionId: string;
  concurrency: number;
  onlyToday: boolean;
  aiTimeout: number;
  autoTargetDate: boolean;
  targetDate: string;
  useRuleFilter: boolean;
  autoRun: boolean;
  runOnStartup: boolean;
  autoRunInterval: number;
  incremental: boolean;
  autoImport: boolean;
  autoExportExcel: boolean;
  exportDir: string;
}

// 本机当天 YYYY-MM-DD，用于「跟随今天」的默认值与日期上限
const todayStr = () => {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
};

const defaults: WechatOrderConfig = {
  group: '帅涛吃什么',
  senders: '帅涛,aemiser',
  days: 2,
  dataDir: '',
  workDir: '',
  dataKey: '',
  platform: 'windows',
  version: 0, // 0 = 自动跟随微信客户端
  autoKey: true,
  keyProviderPath: '',
  providerTimeout: 120,
  autoKeyOnFailure: true,
  wcdbLibPath: '',
  sessionId: '',
  concurrency: 100,
  onlyToday: false,
  aiTimeout: 90,
  // 目标订单日期默认跟随今天（每次启动自动取当天）；粗筛默认开启
  autoTargetDate: true,
  targetDate: todayStr(),
  useRuleFilter: true,
  // 产品语义固定：只做识别、结果进预览区，这些字段仅为兼容旧配置文件保留
  autoRun: false,
  runOnStartup: false,
  autoRunInterval: 30,
  incremental: false,
  autoImport: false,
  autoExportExcel: false,
  exportDir: '',
};

const cfg = ref<WechatOrderConfig>({ ...defaults });
const configPath = ref('');
const notice = ref<{ ok: boolean; text: string } | null>(null);
const isSaving = ref(false);
const isTesting = ref(false);
const testResult = ref<any>(null);
const keyCacheInfo = ref<any>(null);
const keyStatus = ref<{ level: string; text: string } | null>(null);
const isFetchingKey = ref(false);
const keyDropStatus = ref<any>(null);
let unsubKeyStatus: (() => void) | null = null;

interface CfgSyncStatus {
  enabled: boolean;
  intervalSeconds: number;
  checks: number;
  changes: number;
  lastCheckAt: string;
  lastChangedAt: string;
  lastChange: string;
  nextCheckAt: string;
  lastError: string;
  revision: string;
  cfgMd5: string;
  fastSkips: number;
  fullLoads: number;
  partialLoads: number;
  lastSections: string;
  lastMode: string;
}

const cfgSync = ref<CfgSyncStatus | null>(null);
const isSyncing = ref(false);
let unsubCfgReload: (() => void) | null = null;
let syncStatusTimer: number | null = null;

const app = () => (window as any)['go']['main']['App'];

// ---- 订单操作术语规则（与人工审核区快捷入口共用 ActionRuleModal） ----
const actionRules = ref<OrderActionRule[]>([]);
const showActionRuleModal = ref(false);
const editingActionRule = ref<OrderActionRule | null>(null);
const actionRuleView = ref<'list' | 'detail' | 'form'>('list');

const loadActionRules = async () => {
  try {
    actionRules.value = await listActionRules();
  } catch (e: any) {
    notice.value = { ok: false, text: '读取订单操作术语失败：' + (e?.message || e) };
  }
};
const openNewActionRule = () => {
  editingActionRule.value = null;
  actionRuleView.value = 'form';
  showActionRuleModal.value = true;
};
const openDetailActionRule = (r: OrderActionRule) => {
  editingActionRule.value = r;
  actionRuleView.value = 'detail';
  showActionRuleModal.value = true;
};
const openEditActionRule = (r: OrderActionRule) => {
  editingActionRule.value = r;
  actionRuleView.value = 'form';
  showActionRuleModal.value = true;
};
// 弹窗内部保存后自己切回列表 / 详情，这里只刷新表格与提示，不要关掉弹窗。
const handleActionRuleSaved = async () => {
  await loadActionRules();
  notice.value = { ok: true, text: '订单操作术语已保存，下一轮「一键识别订单」立即生效' };
};
// 与弹窗内保持一致：乐观切换，不等回读，避免复选框短暂弹回旧值。
const toggleActionRule = (r: OrderActionRule) => {
  const current = actionRules.value.find(x => x.id === r.id);
  const next = !(current?.enabled ?? r.enabled);
  actionRules.value = actionRules.value.map(x => x.id === r.id ? { ...x, enabled: next } : x);
  void (async () => {
    try {
      const msg = await saveActionRule({ ...r, enabled: next });
      if (msg) throw new Error(msg);
    } catch (e: any) {
      actionRules.value = actionRules.value.map(x => x.id === r.id ? { ...x, enabled: !next } : x);
      notice.value = { ok: false, text: '切换启用状态失败，已恢复：' + (e?.message || e) };
    }
  })();
};
const removeActionRule = async (r: OrderActionRule) => {
  if (!window.confirm(`确定删除术语「${r.keyword}」吗？`)) return;
  try {
    const msg = await deleteActionRule(r.id);
    if (msg) {
      notice.value = { ok: false, text: msg };
      return;
    }
    await loadActionRules();
    notice.value = { ok: true, text: '已删除术语' };
  } catch (e: any) {
    notice.value = { ok: false, text: '删除失败：' + (e?.message || e) };
  }
};

const loadConfig = async () => {
  try {
    const data = await app()['GetWechatOrderConfig']();
    cfg.value = { ...defaults, ...(data || {}) };
    configPath.value = data?.configPath || '';
  } catch (e: any) {
    notice.value = { ok: false, text: '读取配置失败：' + (e?.message || e) };
  }
};

const saveSettings = async () => {
  isSaving.value = true;
  notice.value = null;
  try {
    // 目标订单日期只在「一键识别订单」按钮旁按次选择，配置里始终跟随今天，
    // 避免旧配置文件里残留的历史日期影响测试导出等兼容入口。
    cfg.value.autoTargetDate = true;
    cfg.value.targetDate = todayStr();
    const res = await app()['SaveWechatOrderConfig'](cfg.value);
    notice.value = { ok: !String(res).includes('失败'), text: String(res) };
    await loadConfig();
  } catch (e: any) {
    notice.value = { ok: false, text: '保存失败：' + (e?.message || e) };
  } finally {
    isSaving.value = false;
  }
};

const resetDefaults = () => {
  cfg.value = { ...defaults };
  notice.value = { ok: true, text: '已恢复默认值，点击「保存所有配置」后生效' };
};

const detectWechatData = async () => {
  try {
    const r = await app()['DetectWechatData']();
    if (r?.success) {
      cfg.value.dataDir = r.dataDir || '';
      cfg.value.platform = r.platform || 'windows';
      cfg.value.version = Number(r.version) || 3;
      notice.value = { ok: true, text: (r.message || '探测成功') + '，记得点击保存配置' };
    } else {
      notice.value = { ok: false, text: r?.message || '未检测到微信客户端，请手动填写数据目录与密钥' };
    }
  } catch (e: any) {
    notice.value = { ok: false, text: '探测失败：' + (e?.message || e) };
  }
};

const loadKeyCacheInfo = async () => {
  try {
    keyCacheInfo.value = await app()['GetWechatKeyCacheInfo']();
  } catch {
    keyCacheInfo.value = null;
  }
};

const clearKeyCache = async () => {
  try {
    const msg = await app()['ClearWechatKeyCache']();
    notice.value = { ok: true, text: String(msg) };
    await loadKeyCacheInfo();
  } catch (e: any) {
    notice.value = { ok: false, text: '清除失败：' + (e?.message || e) };
  }
};

const fetchKey = async () => {
  isFetchingKey.value = true;
  keyStatus.value = null;
  try {
    const r = await app()['AutoFetchWechatKey']();
    if (r?.success) {
      keyStatus.value = { level: 'ok', text: r.message || '密钥已获取并缓存' };
      await loadKeyCacheInfo();
    } else {
      keyStatus.value = { level: 'warn', text: r?.message || '获取失败，请手动粘贴密钥' };
    }
  } catch (e: any) {
    keyStatus.value = { level: 'warn', text: '获取异常：' + (e?.message || e) };
  } finally {
    isFetchingKey.value = false;
  }
};

const testExport = async () => {
  isTesting.value = true;
  testResult.value = null;
  notice.value = null;
  try {
    // 先保存，确保测试用的是界面上刚改过的参数
    await app()['SaveWechatOrderConfig'](cfg.value);
    testResult.value = await app()['TestWechatExport']();
    await loadKeyCacheInfo();
  } catch (e: any) {
    testResult.value = { success: false, message: '测试异常：' + (e?.message || e) };
  } finally {
    isTesting.value = false;
  }
};

const loadKeyDropStatus = async () => {
  try {
    keyDropStatus.value = await app()['GetWechatKeyDropStatus']();
  } catch {
    keyDropStatus.value = null;
  }
};

const openKeyDropDir = async () => {
  try {
    await app()['OpenWechatKeyDropDir']();
    notice.value = { ok: true, text: '已打开自动密钥目录，把取密钥工具的输出文件放进去即可' };
  } catch (e: any) {
    notice.value = { ok: false, text: '打开目录失败：' + (e?.message || e) };
  }
};

const loadCfgSyncStatus = async () => {
  try {
    cfgSync.value = await app()['GetCfgSyncStatus']();
  } catch { /* 状态展示失败不影响配置页其它功能 */ }
};

const refreshCfgSync = async () => {
  isSyncing.value = true;
  try {
    const r = await app()['RefreshDBCfg']();
    cfgSync.value = r?.status || null;
    if (r?.error) {
      notice.value = { ok: false, text: '同步失败：' + r.error };
    } else if (r?.changed) {
      notice.value = { ok: true, text: '检测到数据库配置变化：' + (r.summary || '配置已更新') + '，已自动生效，无需重启' };
    } else {
      notice.value = { ok: true, text: '已检查数据库配置，当前无差异（未读取任何配置表）' };
    }
  } catch (e: any) {
    notice.value = { ok: false, text: '同步失败：' + (e?.message || e) };
  } finally {
    isSyncing.value = false;
  }
};

onMounted(async () => {
  await loadConfig();
  await loadKeyCacheInfo();
  await loadKeyDropStatus();
  await loadCfgSyncStatus();
  await loadActionRules();
  unsubKeyStatus = EventsOn('wechat-key-status', (p: any) => {
    if (!p) return;
    const level = p.level === 'ok' ? 'ok' : p.level === 'warn' ? 'warn' : 'info';
    keyStatus.value = { level, text: p.message || '' };
    if (level === 'ok') {
      loadKeyCacheInfo();
    }
  });
  // 后台协程发现数据库配置变化时推事件：立刻刷新状态并提示，无需重启窗口
  unsubCfgReload = EventsOn('cfg-reloaded', (p: any) => {
    loadCfgSyncStatus();
    loadActionRules();
    if (p?.changed) {
      notice.value = { ok: true, text: '检测到数据库配置变化：' + (p.summary || '配置已更新') + '，已自动生效，无需重启' };
    }
  });
  // 定时刷新「上次检查 / 下次检查」的展示（只拉状态，不重复拉配置）
  syncStatusTimer = window.setInterval(loadCfgSyncStatus, 15000);
});

onUnmounted(() => {
  if (unsubKeyStatus) {
    unsubKeyStatus();
    unsubKeyStatus = null;
  }
  if (unsubCfgReload) {
    unsubCfgReload();
    unsubCfgReload = null;
  }
  if (syncStatusTimer !== null) {
    window.clearInterval(syncStatusTimer);
    syncStatusTimer = null;
  }
});
</script>

<template>
  <div class="max-w-5xl mx-auto space-y-6 pb-10">
    <div class="flex flex-col gap-2">
      <h2 class="text-ink font-semibold text-2xl">应用设置 · 微信订单识别</h2>
      <p class="text-ink-2">自动取密钥 → 解密 → 导出群聊 → 规则粗筛 → AI 识别；识别结果只进入数据预览区，由你确认后才入库。</p>
    </div>

    <!-- 识别行为说明 -->
    <section class="p-8 space-y-4 settings-card rounded-lg">
      <div class="flex items-start gap-4">
        <div class="flex items-center justify-center text-2xl text-emerald-500 w-12 h-12 rounded-lg bg-success/10">
          <MessageSquareMore class="w-6 h-6" />
        </div>
        <div class="flex-1">
          <h3 class="text-xl font-bold font-display">识别结果去向</h3>
          <p class="text-sm text-ink-3 mt-1 leading-relaxed">
            「一键识别订单」<b class="text-ink-2">只做信息识别</b>：按「目标订单日期」导出窗口内的群聊，
            日期标记与该日期一致的订单才自动填入数据预览区；没写日期、日期不符或判定存疑的订单
            会进入同页的「待人工审查」区，由你结合发送时间核对后手动点「加入预览区」。
            不写入数据库、不生成 Excel、不后台定时运行，也没有增量水位线或跨轮去重限制，
            按钮可随时重复点击，每次都是一次独立的全量识别。
            确认数据无误后，再由你点右侧「确认数据无误」主动入库。
          </p>
        </div>
      </div>
    </section>

    <!-- 自动密钥目录 -->
    <section class="p-8 space-y-5 settings-card rounded-lg">
      <div class="flex items-start gap-4">
        <div class="flex items-center justify-center text-2xl text-blue-500 w-12 h-12 rounded-lg bg-brand/10">
          <KeyRound class="w-6 h-6" />
        </div>
        <div class="flex-1">
          <h3 class="text-xl font-bold font-display">零配置自动取密钥 · 启动即完成</h3>
          <p class="text-sm text-ink-3 mt-1">
            软件启动时自动从运行中的微信进程<b class="text-ink-2">只读内存</b>里提取 WCDB Config.Cipher 混淆密钥，
            逐个用真实数据库第一页做 HMAC 验签后写入本地缓存。本机微信 <b class="text-ink-2">4.2.0.13</b> 实测约 4 秒取全 23 个库密钥，
            <b class="text-ink-2">无需投放文件、无需外部程序、无需管理员权限、无需任何配置</b>。
            下方「自动密钥目录」仅是未来微信改版时的兜底通道，正常情况完全用不到。
          </p>
        </div>
      </div>
      <div class="flex items-center gap-4 px-5 py-4 rounded-lg glass-input">
        <FolderOpen class="w-4 h-4 text-brand shrink-0" />
        <div class="flex-1 text-xs text-ink-2 break-all leading-relaxed">
          <div>{{ keyDropStatus?.dir || '（读取中）' }}</div>
          <div v-if="keyDropStatus?.files?.length" class="text-ink-3 mt-1">已放入：{{ keyDropStatus.files.join('、') }}</div>
          <div v-else class="text-ink-3 mt-1">目录为空（正常）：仅当自动取密钥失效时才需要投放密钥文件</div>
          <div v-if="keyDropStatus?.error || keyDropStatus?.writable === false" class="text-danger-ink mt-1">
            ⚠️ 自动密钥目录异常：{{ keyDropStatus?.error || '目录不可写' }}。把密钥文件放进此目录后可能无法被读取，请检查权限或磁盘是否只读。
          </div>
        </div>
        <button @click="openKeyDropDir" class="px-4 py-2 rounded-md bg-brand-soft hover:bg-brand/30 border border-blue-500/30 text-blue-200 text-xs font-medium shrink-0 transition-all">打开目录</button>
        <button @click="loadKeyDropStatus" class="px-4 py-2 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs font-medium shrink-0 transition-all">刷新</button>
      </div>
      <p class="text-xs text-ink-3">
        支持格式：按库密钥 JSON（含 enc_key）、带 key / db_key 字段的 JSON、纯 64 位 hex 文本、x'&lt;hex&gt;' 日志片段。
        不信任文件内容，一律先用本机数据库第一页校验通过才写入缓存。
      </p>
    </section>

    <!-- 本地微信数据库导出（chatlog 核心） -->
    <section class="p-8 space-y-6 settings-card rounded-lg">
      <div class="flex items-start gap-4">
        <div class="flex items-center justify-center text-2xl text-blue-500 w-12 h-12 rounded-lg bg-brand/10">
          <FolderOpen class="w-6 h-6" />
        </div>
        <div class="flex-1">
          <h3 class="text-xl font-bold font-display">本地微信数据库导出</h3>
          <p class="text-sm text-ink-3 mt-1">
            聊天记录由项目内移植的 chatlog 核心 Go 代码读取本地微信数据库。
            密钥优先用「手动配置 → 本地缓存」，都没有时才按需调用下方组件或做只读内存扫描；
            随后解密数据库、按群名与时间查询消息。不启动 HTTP 服务、不操作微信窗口。
          </p>
        </div>
      </div>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-5">
        <div class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">微信数据目录（加密数据库所在目录）</span>
          <div class="flex gap-3">
            <input v-model="cfg.dataDir" type="text" class="setting-input flex-1" placeholder="留空 = 自动探测运行中的微信客户端" />
            <button
              @click="detectWechatData"
              class="px-6 rounded-lg bg-subtle hover:bg-hover border border-line text-ink-2 text-sm font-medium shrink-0 transition-all"
            >
              探测
            </button>
          </div>
          <span class="text-xs text-ink-3">点「探测」读取当前登录微信的数据目录、平台与版本；也可手动填写账号根目录：3.9 是 WeChat Files/wxid_xxx（内含 Msg），4.x 是 xwechat_files/wxid_xxx（内含 db_storage）。程序会按目录布局自动校正版本，换微信版本后无需重填。</span>
        </div>
        <label class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">解密输出目录（可选）</span>
          <input v-model="cfg.workDir" type="text" class="setting-input" placeholder="留空 = %APPDATA%/order_server/wechat_decrypted" />
          <span class="text-xs text-ink-3">解密后的数据库按原目录结构存到这里；已解密且比源文件新的文件会自动跳过。</span>
        </label>
        <label class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">数据库密钥（hex 或按库密钥 JSON，可选）</span>
          <textarea v-model="cfg.dataKey" rows="3" class="setting-input font-mono text-xs" placeholder="64 位 hex 密钥，或按库密钥 JSON"></textarea>
          <span class="text-xs text-ink-3">微信 3.9 全库共用一把 64 位 hex 密钥（自动从进程内存提取，最新消息也会从 WAL 合并）；微信 4.x 每个数据库密钥不同，可粘贴 wx_key / EchoTrace 等工具导出的密钥（单个 hex 或按库 JSON）。更省事的做法是把工具输出文件放进上方「自动密钥目录」，程序会自动导入并缓存。若已有解密好的数据库，可关闭自动提取并留空密钥，直接读取解密输出目录。</span>
        </label>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">平台</span>
          <input v-model="cfg.platform" type="text" class="setting-input" placeholder="windows" />
        </label>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">微信版本</span>
          <select v-model.number="cfg.version" class="setting-input">
            <option :value="0">自动（跟随运行中的微信客户端）</option>
            <option :value="3">3.x（微信 3.9）</option>
            <option :value="4">4.x（微信 4.0+）</option>
          </select>
        </label>
        <div class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">自动提取密钥</span>
          <button
            type="button"
            @click="cfg.autoKey = !cfg.autoKey"
            class="w-full flex items-center justify-between px-5 py-4 rounded-lg glass-input text-sm"
          >
            <span :class="cfg.autoKey ? 'text-success-ink' : 'text-ink-2'">
              {{ cfg.autoKey ? '开启：密钥兜底时只读扫描微信进程内存' : '关闭：仅使用手动密钥 / 缓存 / 外部组件 / 已解密数据库' }}
            </span>
            <span class="relative w-11 h-6 rounded-full transition-colors" :class="cfg.autoKey ? 'bg-success' : 'bg-hover'">
              <span class="absolute top-0.5 w-5 h-5 rounded-full bg-surface transition-all" :class="cfg.autoKey ? 'left-6' : 'left-0.5'"></span>
            </span>
          </button>
        </div>
      </div>
    </section>

    <!-- WeFlow 式可插拔组件 -->
    <section class="p-8 space-y-6 settings-card rounded-lg">
      <div class="flex items-start gap-4">
        <div class="flex items-center justify-center text-2xl text-brand w-12 h-12 rounded-lg bg-brand-soft">
          <ShieldCheck class="w-6 h-6" />
        </div>
        <div class="flex-1">
          <h3 class="text-xl font-bold font-display">可插拔组件 · 只读安全说明（参考 WeFlow）</h3>
          <p class="text-sm text-ink-3 mt-1">
            与 WeFlow 一致：密钥获取和读库能力可外包给用户自备组件，<b class="text-ink-2">留空即不加载、不拉起、不触碰微信进程</b>。
            内置只读内存扫描在软件启动时自动取密钥并缓存；点「一键识别订单」时才做「离线解密 → 规则粗筛 → 并发 DeepSeek 识别」，
            结果只进入数据预览区，不入库、不生成文件。
          </p>
        </div>
      </div>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-5">
        <label class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">外部密钥提供者（keyProviderPath，可选）</span>
          <input v-model="cfg.keyProviderPath" type="text" class="setting-input" placeholder="留空 = 只用手动密钥 / 本地缓存" />
          <span class="text-xs text-ink-3">
            可执行文件，按 WeFlow 的 NDJSON 协议通信：stdin 一行 get_db_key 请求，stdout 逐行输出 progress 事件，
            最后一条 result 带 success 与 key。程序以隐藏窗口方式拉起，不激活微信窗口；找不到文件会明确报「未配置」，无隐式降级。
          </span>
        </label>
        <label class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">WCDB 动态库（wcdbLibPath，可选）</span>
          <input v-model="cfg.wcdbLibPath" type="text" class="setting-input" placeholder="留空 = 用内置离线解密链路" />
          <span class="text-xs text-ink-3">
            用户自备的 dll/dylib，按 WeFlow 组件协议提供 wcdb_open_account / wcdb_open_message_cursor / wcdb_fetch_message_batch。
            配置后直接打开加密库按时间游标批量读，跳过全量解密；留空时程序完全不加载任何 DLL。
          </span>
        </label>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">群会话 ID（配合 WCDB，可选）</span>
          <input v-model="cfg.sessionId" type="text" class="setting-input" placeholder="留空 = 按群名自动匹配会话" />
          <span class="text-xs text-ink-3">形如 12345678@chatroom；填写后可跳过会话列表查询。</span>
        </label>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">密钥提供者超时（秒）</span>
          <input v-model.number="cfg.providerTimeout" type="number" min="5" max="600" class="setting-input" />
          <span class="text-xs text-ink-3">默认 120 秒，与 WeFlow 一致。</span>
        </label>
        <div class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">密钥缓存失效时自动重取</span>
          <button
            type="button"
            @click="cfg.autoKeyOnFailure = !cfg.autoKeyOnFailure"
            class="w-full flex items-center justify-between px-5 py-4 rounded-lg glass-input text-sm"
          >
            <span :class="cfg.autoKeyOnFailure ? 'text-success-ink' : 'text-ink-2'">
              {{ cfg.autoKeyOnFailure ? '开启：新增数据库分片 / 解密校验失败时重新取密钥' : '关闭：缓存失效后需手动清除缓存或重新粘贴密钥' }}
            </span>
            <span class="relative w-11 h-6 rounded-full transition-colors" :class="cfg.autoKeyOnFailure ? 'bg-success' : 'bg-hover'">
              <span class="absolute top-0.5 w-5 h-5 rounded-full bg-surface transition-all" :class="cfg.autoKeyOnFailure ? 'left-6' : 'left-0.5'"></span>
            </span>
          </button>
        </div>
        <div class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">本地密钥缓存</span>
          <div class="flex items-center gap-4 px-5 py-4 rounded-lg glass-input">
            <KeyRound class="w-4 h-4 text-success shrink-0" />
            <div class="flex-1 text-xs text-ink-2 leading-relaxed break-all">
              <div v-if="keyCacheInfo?.exists">
                已缓存 · 来源 {{ keyCacheInfo.source || '未知' }} · {{ keyCacheInfo.updatedAt || '' }}<br />{{ keyCacheInfo.path }}
              </div>
              <div v-else>
                暂无缓存。首次取到密钥后自动写入 {{ keyCacheInfo?.path || '用户配置目录' }}（仅本地、权限 0600），之后导出不再需要取密钥。
              </div>
            </div>
            <button
              @click="fetchKey"
              :disabled="isFetchingKey"
              class="px-4 py-2 rounded-md bg-brand-soft hover:bg-brand/30 border border-blue-500/30 text-blue-200 text-xs font-medium shrink-0 transition-all flex items-center gap-1.5 disabled:opacity-50"
            >
              <Loader2 v-if="isFetchingKey" class="w-3.5 h-3.5 animate-spin" />
              <KeyRound v-else class="w-3.5 h-3.5" />
              获取密钥
            </button>
            <button
              @click="clearKeyCache"
              class="px-4 py-2 rounded-md bg-subtle hover:bg-hover border border-line text-ink-2 text-xs font-medium shrink-0 transition-all flex items-center gap-1.5"
            >
              <Trash2 class="w-3.5 h-3.5" /> 清除缓存
            </button>
          </div>
          <p v-if="keyStatus" class="text-xs" :class="{
            'text-success-ink': keyStatus.level === 'ok',
            'text-amber-300': keyStatus.level === 'warn',
            'text-ink-2': keyStatus.level === 'info'
          }">
            {{ keyStatus.text }}
          </p>
          <p v-if="keyCacheInfo?.dirError || keyCacheInfo?.error" class="text-xs text-danger-ink">
            ⚠️ 密钥缓存目录异常：{{ keyCacheInfo?.dirError || keyCacheInfo?.error }}。密钥将无法保存，下次仍需重新获取；请检查该目录权限或磁盘是否只读。
          </p>
          <span class="text-xs text-ink-3">软件启动且无缓存时会自动从微信进程只读内存提取密钥并验签缓存（内嵌 Config.Cipher 解码，约 4 秒）；点「获取密钥」可手动触发或刷新。若未来微信改版导致自动提取失效，再用上方「自动密钥目录」投放一次密钥文件。</span>
        </div>
      </div>

      <div class="rounded-lg border border-emerald-500/15 bg-success/5 px-5 py-4 text-xs text-ink-2 leading-relaxed">
        <b class="text-success-ink">安全边界：</b>密钥提取只做进程内存<b>只读</b>扫描，不注入 DLL、不写微信内存、不发消息、不联网操作微信；
        解密只处理本机 .db 文件，密钥仅存本机 0600 缓存。整个过程无需外部程序、无需管理员权限。
        微信新增数据库分片、换账号或重装时，程序会按文件指纹判定缓存失效并自动重取。
      </div>
    </section>

    <!-- 群聊与范围 -->
    <section class="p-8 space-y-6 settings-card rounded-lg">
      <div class="flex items-start gap-4">
        <div class="flex items-center justify-center text-2xl text-emerald-500 w-12 h-12 rounded-lg bg-success/10">
          <MessageSquareMore class="w-6 h-6" />
        </div>
        <div class="flex-1">
          <h3 class="text-xl font-bold font-display">群聊与顾客</h3>
          <p class="text-sm text-ink-3 mt-1">按群名与发言人昵称过滤导出的消息；昵称支持子串匹配、多个用逗号分隔。</p>
        </div>
      </div>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-5">
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">群聊名称</span>
          <input v-model="cfg.group" type="text" class="setting-input" placeholder="帅涛吃什么" />
        </label>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">顾客 / 下单人昵称</span>
          <input v-model="cfg.senders" type="text" class="setting-input" placeholder="帅涛,aemiser" />
          <span class="text-xs text-ink-3">留空 = 不限制发言人。当前群内订单由「帅涛」发出，默认同时兼容 aemiser。</span>
        </label>
        <!-- 目标订单日期已移到「一键识别订单」按钮旁，按次选择、无需保存 -->
        <div class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">
            <CalendarDays class="w-3.5 h-3.5 inline -mt-0.5 mr-1" />要识别的订单日期
          </span>
          <div class="px-5 py-4 rounded-lg glass-input text-sm space-y-1.5">
            <p class="text-ink-2">
              该配置已移到「订单输入」页<b class="text-ink">「一键识别订单」按钮旁边</b>：可直接选日期，或用左右箭头切换前一天 / 后一天，改完点按钮立即生效。
            </p>
            <p class="text-xs text-ink-3 leading-relaxed">
              每次启动默认今天；所选日期只对当次识别生效，不写入配置文件，不影响下次启动。订单消息首行一般带「11-7」这类日期标记，
              系统以所选日期为终点导出前 N 天聊天记录，再按日期标记筛出属于该日的订单；没写日期的订单也能识别出来，但会进入人工审查区。
            </p>
          </div>
        </div>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">
            <CalendarDays class="w-3.5 h-3.5 inline -mt-0.5 mr-1" />导出天数
          </span>
          <input v-model.number="cfg.days" type="number" min="1" max="30" class="setting-input" />
          <span class="text-xs text-ink-3">默认 2 = 目标日期当天 + 前一天。顾客常在昨天发今天的订单，所以至少导出两天；窗口终点是目标订单日期，不是固定今天。</span>
        </label>
        <div class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">日期不符订单的处理</span>
          <button
            type="button"
            @click="cfg.onlyToday = !cfg.onlyToday"
            class="w-full flex items-center justify-between px-5 py-4 rounded-lg glass-input text-sm"
          >
            <span :class="cfg.onlyToday ? 'text-success-ink' : 'text-ink-2'">
              {{ cfg.onlyToday ? '严格模式：日期不符的订单直接丢弃' : '宽松模式：日期不符的订单进人工审查区' }}
            </span>
            <span class="relative w-11 h-6 rounded-full transition-colors" :class="cfg.onlyToday ? 'bg-success' : 'bg-hover'">
              <span class="absolute top-0.5 w-5 h-5 rounded-full bg-surface transition-all" :class="cfg.onlyToday ? 'left-6' : 'left-0.5'"></span>
            </span>
          </button>
          <span class="text-xs text-ink-3">无论哪种模式，日期不符的订单都不会自动进入数据预览区；宽松模式只是把它保留给人工判断，避免丢单。</span>
        </div>
      </div>
    </section>

    <!-- 分析引擎 -->
    <section class="p-8 space-y-6 settings-card rounded-lg">
      <div class="flex items-start gap-4">
        <div class="flex items-center justify-center text-2xl text-teal-500 w-12 h-12 rounded-lg bg-teal-600/10">
          <Cpu class="w-6 h-6" />
        </div>
        <div class="flex-1">
          <h3 class="text-xl font-bold font-display">规则粗筛 + DeepSeek 并发分析</h3>
          <p class="text-sm text-ink-3 mt-1">
            粗筛只挡明显不是订单的消息，不误杀无标签订单；候选逐条交给 DeepSeek，
            非订单返回 is_order=false，订单返回系统现有的订单 JSON 字段（收件人 / 手机号 / 收货地址 / 商品规格 / 备注），
            并额外给出日期是否确认、是否需要人工审查等判定字段。
          </p>
        </div>
      </div>
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-5">
        <div class="space-y-2 lg:col-span-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">规则粗筛开关</span>
          <button
            type="button"
            @click="cfg.useRuleFilter = !cfg.useRuleFilter"
            class="w-full flex items-center justify-between px-5 py-4 rounded-lg glass-input text-sm"
          >
            <span :class="cfg.useRuleFilter ? 'text-success-ink' : 'text-ink-2'">
              {{ cfg.useRuleFilter ? '开启：先粗筛掉明显非订单，再把候选交 AI' : '关闭：所有聊天消息直接全由 AI 识别' }}
            </span>
            <span class="relative w-11 h-6 rounded-full transition-colors" :class="cfg.useRuleFilter ? 'bg-success' : 'bg-hover'">
              <span class="absolute top-0.5 w-5 h-5 rounded-full bg-surface transition-all" :class="cfg.useRuleFilter ? 'left-6' : 'left-0.5'"></span>
            </span>
          </button>
          <span class="text-xs text-ink-3">
            关闭后不再过滤任何闲聊，群里每条消息都会请求 AI（调用量与耗时明显增加）。
            同时提示词会自动切换为「全量消息模式」，明确给出「订单数据 / 聊天数据」的判定依据：
            要求同时具备可配送对象与商品行才判为订单，寒暄、播报、群游戏、机器人回显等一律判为非订单。
          </span>
        </div>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">并发协程数</span>
          <input v-model.number="cfg.concurrency" type="number" min="1" max="500" class="setting-input" />
          <span class="text-xs text-ink-3">默认 100 个协程并发请求 DeepSeek。</span>
        </label>
        <label class="space-y-2">
          <span class="text-xs font-bold text-ink-3 uppercase tracking-widest">单条 AI 请求超时（秒）</span>
          <input v-model.number="cfg.aiTimeout" type="number" min="10" max="600" class="setting-input" />
        </label>
      </div>
    </section>

    <!-- 订单操作术语规则 -->
    <section class="p-8 space-y-5 settings-card rounded-lg">
      <div class="flex items-start gap-4 flex-wrap">
        <div class="flex items-center justify-center w-12 h-12 rounded-lg bg-brand/10 shrink-0">
          <Tags class="w-6 h-6 text-brand-ink" />
        </div>
        <div class="flex-1 min-w-0">
          <h3 class="text-xl font-bold font-display">订单操作术语 · 口语映射</h3>
          <p class="text-sm text-ink-3 mt-1 leading-relaxed">
            群里说「华夏农业大学消单」「这单发小果」时，大模型未必懂「消单 = 取消订单」。
            在这里把口语映射到删除 / 修改动作，系统会自动把规则写进识别提示词、放进候选筛选，
            并在模型判不出来时兜底。<b class="text-ink-2">加词后无需重启、无需发版</b>。
          </p>
        </div>
        <button
          @click="openNewActionRule"
          class="px-4 py-2 rounded-lg bg-brand hover:bg-brand-hover text-onbrand text-xs font-bold shrink-0 transition-all flex items-center gap-1.5"
        >
          <Plus class="w-3.5 h-3.5" /> 新增术语
        </button>
      </div>

      <div class="rounded-lg border border-line overflow-x-auto custom-scrollbar">
        <table class="w-full text-xs min-w-[760px]">
          <thead class="bg-subtle text-ink-2">
            <tr>
              <th class="px-3 py-2.5 text-left font-medium">触发关键词</th>
              <th class="px-3 py-2.5 text-left font-medium">订单操作</th>
              <th class="px-3 py-2.5 text-left font-medium">字段 / 新值抽取</th>
              <th class="px-3 py-2.5 text-left font-medium">作用范围</th>
              <th class="px-3 py-2.5 text-left font-medium">优先级</th>
              <th class="px-3 py-2.5 text-center font-medium">启用</th>
              <th class="px-3 py-2.5 text-right font-medium">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in actionRules" :key="r.id"
                @click="openDetailActionRule(r)"
                class="border-t border-line hover:bg-hover/40 transition-colors cursor-pointer"
                title="点击查看这条术语的完整详情">
              <td class="px-3 py-2.5">
                <div class="font-medium text-ink">{{ r.keyword }}</div>
                <div v-if="r.remark" class="text-[11px] text-ink-3 mt-0.5">{{ r.remark }}</div>
              </td>
              <td class="px-3 py-2.5">
                <span :class="['px-2 py-0.5 rounded text-[11px] font-medium',
                  r.action === 'delete' ? 'bg-danger-soft text-danger-ink' :
                  r.action === 'modify' ? 'bg-brand-soft text-brand-ink' : 'bg-subtle text-ink-2']">
                  {{ actionLabel(r.action) }}
                </span>
              </td>
              <td class="px-3 py-2.5 text-ink-2">
                <template v-if="r.action === 'modify'">{{ fieldLabel(r.field) }} · {{ valueRuleLabel(r.valueRule) }}</template>
                <span v-else class="text-ink-3">—</span>
              </td>
              <td class="px-3 py-2.5 text-ink-3">{{ scopeLabel(r.scope) }}</td>
              <td class="px-3 py-2.5 text-ink-2 font-mono">{{ r.priority }}</td>
              <!-- 启用列整格是开关热区：点这一格任意位置只切换状态，不会误跳详情页 -->
              <td class="p-0" @click.stop>
                <label class="flex items-center justify-center w-full min-h-[40px] cursor-pointer hover:bg-hover/70 transition-colors"
                       title="点击这一格切换启用状态">
                  <input type="checkbox" :checked="r.enabled" @change.stop="toggleActionRule(r)"
                         class="w-4 h-4 accent-brand cursor-pointer" />
                </label>
              </td>
              <td class="px-3 py-2.5 text-right whitespace-nowrap">
                <button @click.stop="openEditActionRule(r)"
                        class="px-2 py-1 rounded-md text-ink-2 hover:bg-hover border border-line text-[11px] inline-flex items-center gap-1 transition-colors">
                  <PencilLine class="w-3 h-3" /> 编辑
                </button>
                <button @click.stop="removeActionRule(r)"
                        class="ml-1.5 px-2 py-1 rounded-md text-danger-ink hover:bg-danger-soft border border-line text-[11px] inline-flex items-center gap-1 transition-colors">
                  <Trash2 class="w-3 h-3" /> 删除
                </button>
              </td>
            </tr>
            <tr v-if="!actionRules.length">
              <td colspan="7" class="px-4 py-6 text-center text-ink-3">
                还没有配置术语。点右上角「新增术语」，把「消单」映射到「删除订单」即可。
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <p class="text-xs text-ink-3 leading-relaxed">
        点击表格任意一行可在弹窗里查看完整详情并编辑（弹窗内也有术语列表，支持悬浮预览省略字段）。
        店名 / 收件人 / 电话这类<b class="text-ink-2">定位线索不用配置</b>：系统会从已识别订单里自动反查
        （「华夏农业大学消单」少一个「店」字也能命中「华夏农业大学店」）。关键词只负责「这是什么操作」；
        改地址、改电话、改备注、改规格等修改动作可以继续新增，规则模板会自动扩展。
      </p>
    </section>

    <ActionRuleModal
      v-if="showActionRuleModal"
      :show="showActionRuleModal"
      :initialView="actionRuleView"
      :rule="editingActionRule"
      @close="showActionRuleModal = false"
      @saved="handleActionRuleSaved"
    />

    <!-- 数据库配置自动同步 -->
    <section class="p-8 space-y-5 settings-card rounded-lg">
      <div class="flex items-start gap-4">
        <div class="flex items-center justify-center text-2xl text-indigo-500 w-12 h-12 rounded-lg bg-brand/10">
          <Database class="w-6 h-6" />
        </div>
        <div class="flex-1">
          <h3 class="text-xl font-bold font-display">数据库配置 · 自动同步</h3>
          <p class="text-sm text-ink-3 mt-1">
            全部 AI 提示词、模型参数、商品归属与通用设置都存在数据库的 cfg_* 表里。
            应用每 <b class="text-ink-2">{{ cfgSync?.intervalSeconds || 60 }} 秒</b>让数据库分别算一次五张表的 MD5，
            <b class="text-ink-2">哪张表不一样就只拉哪张表</b>，没变化的配置不经过网络；
            发现改动就整体替换内存配置并在这里提示，<b class="text-ink-2">已启动的窗口无需重启即可生效</b>。
          </p>
        </div>
        <button
          @click="refreshCfgSync"
          :disabled="isSyncing"
          class="px-5 py-2.5 rounded-lg bg-brand-soft hover:bg-brand/30 border border-blue-500/30 text-blue-200 text-xs font-medium shrink-0 transition-all flex items-center gap-1.5 disabled:opacity-50"
        >
          <Loader2 v-if="isSyncing" class="w-3.5 h-3.5 animate-spin" />
          <RefreshCw v-else class="w-3.5 h-3.5" />
          立即同步
        </button>
      </div>

      <div class="grid grid-cols-2 lg:grid-cols-4 gap-4">
        <div class="px-4 py-3 rounded-lg glass-input">
          <div class="text-xs text-ink-3">运行状态</div>
          <div class="text-sm font-medium mt-1" :class="cfgSync?.enabled ? 'text-success-ink' : 'text-warning-ink'">
            {{ cfgSync?.enabled ? '自动检查中' : '未启动' }}
          </div>
        </div>
        <div class="px-4 py-3 rounded-lg glass-input">
          <div class="text-xs text-ink-3">上次检查</div>
          <div class="text-sm font-medium mt-1 text-ink-2">{{ cfgSync?.lastCheckAt || '尚未检查' }}</div>
        </div>
        <div class="px-4 py-3 rounded-lg glass-input">
          <div class="text-xs text-ink-3">下次检查</div>
          <div class="text-sm font-medium mt-1 text-ink-2">{{ cfgSync?.nextCheckAt || '—' }}</div>
        </div>
        <div class="px-4 py-3 rounded-lg glass-input">
          <div class="text-xs text-ink-3">已生效变更</div>
          <div class="text-sm font-medium mt-1 text-ink-2">{{ cfgSync?.changes || 0 }} 次</div>
        </div>
      </div>

      <div class="px-5 py-4 rounded-lg glass-input text-xs text-ink-2 space-y-1.5">
        <div>配置 MD5：<span class="font-mono">{{ cfgSync?.cfgMd5 || '—' }}</span> · 配置版本 <span class="font-mono">{{ cfgSync?.revision || '—' }}</span></div>
        <div>
          累计检查 {{ cfgSync?.checks || 0 }} 次 ·
          <span class="text-success-ink">MD5 快比对跳过 {{ cfgSync?.fastSkips || 0 }} 次</span> ·
          读取配置表 {{ cfgSync?.fullLoads || 0 }} 次（其中<span class="text-success-ink">增量 {{ cfgSync?.partialLoads || 0 }} 次</span>） · 本轮 {{ cfgSync?.lastMode || '—' }}
        </div>
        <div>本轮读取的表：<span class="font-mono">{{ cfgSync?.lastSections || '—' }}</span></div>
        <div v-if="cfgSync?.lastChange">最近变更：{{ cfgSync.lastChange }}（{{ cfgSync.lastChangedAt }}）</div>
        <div v-else>最近变更：暂无</div>
        <div v-if="cfgSync?.lastError" class="text-danger-ink">⚠️ 上次检查失败：{{ cfgSync.lastError }}</div>
      </div>
      <p class="text-xs text-ink-3 leading-relaxed">
        覆盖范围：cfg_prompts（全部 AI 提示词）、cfg_ai_models（模型地址 / 密钥 / 模型名）、cfg_product_belongs（商品归属）、cfg_order_action_rules（订单操作术语）、cfg_settings（邮箱 / 保存路径 / 登录开关 / 更新镜像）。
        整体配置 MD5 存在 cfg_revision 表里供查看；判定用的是数据库每轮为五张表分别实算的 MD5，
        所以即使用外部 SQL 直接改配置、没维护这张表，一样能检出并自动修正版本行。没变化的表复用上次已加载的行，不会重新传提示词正文。
        本页的微信识别配置存在本机 JSON，每次点「一键识别订单」都会重新读取，天然实时；settings.yaml 里的 log / db 属于连库前的引导配置，改了仍需重启。
        登录开关在数据库里改动后，当前会话不会被强制踢出，下次启动生效。
      </p>
    </section>

    <!-- 提示 -->
    <div v-if="notice" class="flex items-center gap-3 px-5 py-4 rounded-lg border text-sm"
         :class="notice.ok ? 'bg-success-soft border-success-border text-success-ink' : 'bg-danger-soft border-danger-border text-red-200'">
      <CheckCircle2 v-if="notice.ok" class="w-4 h-4" />
      <AlertCircle v-else class="w-4 h-4" />
      {{ notice.text }}
    </div>

    <!-- 测试结果 -->
    <div v-if="testResult" class="p-6 rounded-lg border border-line bg-subtle space-y-2 text-sm">
      <div class="flex items-center gap-2 font-bold" :class="testResult.success ? 'text-success-ink' : 'text-danger-ink'">
        <CheckCircle2 v-if="testResult.success" class="w-4 h-4" />
        <AlertCircle v-else class="w-4 h-4" />
        {{ testResult.message }}
      </div>
      <p v-if="testResult.success" class="text-xs text-ink-2">
        目标订单日期 {{ testResult.targetDate }}（{{ testResult.targetLabel }}） · 来源 {{ testResult.source }} · 窗口 {{ testResult.from }} ~ {{ testResult.to }} ·
        消息 {{ testResult.count }} 条 · {{ testResult.useRuleFilter ? '粗筛保留' : '粗筛关闭，全量交 AI' }} {{ testResult.candidates }} 条 · 跳过 {{ testResult.dropped }} 条
      </p>
      <p v-if="testResult.keySource" class="text-xs text-ink-3">密钥来源：{{ testResult.keySource }} · 缓存文件：{{ testResult.keyCache }}</p>
      <p v-if="testResult.sample" class="text-xs text-ink-3 font-mono truncate">样本：{{ testResult.sample }}</p>
      <p v-for="(w, i) in (testResult.warnings || [])" :key="i" class="text-xs text-warning-ink">· {{ w }}</p>
    </div>

    <!-- Footer Actions -->
    <div class="pt-6 border-t border-line flex flex-wrap items-center justify-between gap-4">
      <div class="flex flex-col gap-1 text-xs text-ink-3 italic">
        <span class="flex items-center gap-2"><Info class="w-4 h-4 text-blue-500" /> 配置文件：{{ configPath || '（首次保存后生成）' }}</span>
        <button @click="resetDefaults" class="flex items-center gap-1.5 text-ink-2 hover:text-ink transition-colors not-italic">
          <RotateCcw class="w-3.5 h-3.5" /> 恢复默认
        </button>
      </div>
      <div class="flex items-center gap-3">
        <button
          @click="testExport"
          :disabled="isTesting || isSaving"
          class="px-6 py-4 rounded-lg bg-subtle hover:bg-hover border border-line text-ink font-medium flex items-center gap-2 transition-all disabled:opacity-50"
        >
          <Loader2 v-if="isTesting" class="w-4 h-4 animate-spin" />
          <FlaskConical v-else class="w-4 h-4" />
          测试导出
        </button>
        <button
          @click="saveSettings"
          :disabled="isSaving"
          class="px-10 py-4 rounded-lg bg-brand hover:bg-brand-hover text-onbrand font-bold shadow-2xl shadow-blue-600/30 transition-all active:scale-95 flex items-center gap-2 disabled:opacity-50"
        >
          <Loader2 v-if="isSaving" class="w-4 h-4 animate-spin" />
          <Save v-else class="w-4 h-4" />
          保存所有配置
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 浅色主题：卡片与输入控件统一由全局设计令牌提供 */
.font-display { font-family: inherit; }
.glass-input {
  background-color: var(--color-subtle);
  border: 1px solid var(--color-line-strong);
  transition: all 0.2s ease;
}
.glass-input:focus {
  outline: none;
  border-color: var(--color-brand);
  background-color: var(--color-surface);
}
.settings-card {
  background: var(--color-surface);
  border: 1px solid var(--color-line);
  box-shadow: var(--shadow-card);
  transition: all 0.3s ease;
}
.settings-card:hover { border-color: var(--color-brand-border); }
.setting-input {
  width: 100%;
  padding: 0.6rem 0.9rem;
  border-radius: var(--radius-md);
  background-color: var(--color-subtle);
  border: 1px solid var(--color-line-strong);
  color: var(--color-ink);
  font-size: 0.875rem;
  outline: none;
  transition: all 0.2s ease;
}
.setting-input:focus { border-color: var(--color-brand); background-color: var(--color-surface); }
.setting-input option { background: var(--color-surface); color: var(--color-ink); }
</style>
