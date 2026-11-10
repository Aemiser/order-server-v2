import { reactive } from 'vue';
import { FetchOrdersByDate } from '../../wailsjs/go/main/App';

// 一键识别订单的全局状态机。
//
// 为什么必须放这里：OrderInputView 整棵子树在 App.vue 里是 v-if 渲染，切到别的 tab
// 就会销毁。旧实现把「正在识别 / 进度 / 结果 / 目标日期」全放在 OrderInput.vue 的
// 组件内 ref，进度事件也只由该组件订阅，切页时：
//   1. onUnmounted 会 EventsOff('wechat-order-progress')，后端仍在发的进度全部丢失；
//   2. 组件内状态归零，切回来看到「空闲」；
//   3. 最要命的是结果靠 emit 上抛，Vue 3 卸载后的组件 emit 直接被吞，
//      导致 Go 侧 global.AddressList 已更新、前端表格还是旧数据。
// 这个模块是模块级单例，不依赖任何组件实例；Wails 的 Promise 在这里兑现，
// 界面切到哪个页面都不影响识别继续执行与结果回填。

export interface RecognitionProgress {
  stage?: string;
  done?: number;
  total?: number;
  message?: string;
}

interface RecognitionState {
  running: number;        // 并发任务数（Go 侧 runMu 会串行排队）
  runId: number;          // 最新任务编号
  lastResultId: number;   // 已回填预览区的结果编号，防旧任务覆盖新结果
  progress: string;       // 最近一条进度文案
  stage: string;
  done: number;
  total: number;
  history: string[];      // 最近若干条进度，切页回来可回看
  summary: any | null;    // 最近一轮 AutoOrderResult
  error: string;
  lastResult: any | null; // 供 App.vue 的常驻 watch 回填 orders / reviewOrders
  targetDate: string;     // 按钮旁的「目标订单日期」，跨 tab 保留
  isIncremental: boolean; // 全量 / 增量开关，跨 tab 保留
  startedAt: number;
  finishedAt: number;
}

export const dateStr = (d: Date) =>
  `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`;
export const today = () => dateStr(new Date());

export const recognitionState = reactive<RecognitionState>({
  running: 0,
  runId: 0,
  lastResultId: 0,
  progress: '',
  stage: '',
  done: 0,
  total: 0,
  history: [],
  summary: null,
  error: '',
  lastResult: null,
  targetDate: today(),
  isIncremental: true,
  startedAt: 0,
  finishedAt: 0,
});

// 由 App.vue（常驻根组件）订阅 wechat-order-progress 后调用。
// 识别没在跑时忽略事件：RunWechatFullScan 等其它链路也会发同名事件，避免串扰。
export function noteRecognitionProgress(p: RecognitionProgress | null) {
  if (!p || recognitionState.running <= 0) return;
  const msg = (p.message || '').trim();
  if (msg) {
    recognitionState.progress = msg;
    const h = recognitionState.history;
    if (h[h.length - 1] !== msg) {
      h.push(msg);
      if (h.length > 50) h.shift();
    }
  }
  recognitionState.stage = p.stage || '';
  recognitionState.done = Number(p.done) || 0;
  recognitionState.total = Number(p.total) || 0;
}

// 触发一轮识别：完全 fire-and-forget，调用方组件随后被销毁也不影响结果。
export function startRecognition(runDate: string, incremental: boolean) {
  const id = ++recognitionState.runId;
  recognitionState.running++;
  recognitionState.startedAt = Date.now();
  recognitionState.finishedAt = 0;
  recognitionState.error = '';
  recognitionState.summary = null;
  recognitionState.history = [];
  recognitionState.progress = incremental
    ? '正在导出微信群聊记录（增量：仅补充今日新订单）…'
    : '正在导出微信群聊记录（全量识别）…';
  recognitionState.stage = 'export';
  recognitionState.done = 0;
  recognitionState.total = 0;

  // 不 await：Wails 绑定返回 Promise，Go 侧任务在后台跑完，回调在模块顶层兑现。
  FetchOrdersByDate(runDate, incremental)
    .then((result: any) => {
      // Go 侧 runMu 串行排队，但多轮 Promise 仍可能乱序兑现：只认编号最大的一轮，
      // 否则旧任务的摘要 / 错误会覆盖新结果，而预览区却已是新数据。
      if (id < recognitionState.lastResultId) return;
      recognitionState.lastResultId = id;
      recognitionState.lastResult = result;
      recognitionState.summary = result;
      recognitionState.error = result?.success ? '' : (result?.message || '未知错误');
    })
    .catch((e: any) => {
      if (id < recognitionState.lastResultId) return;
      recognitionState.error = e?.message || String(e);
    })
    .finally(() => {
      recognitionState.running = Math.max(0, recognitionState.running - 1);
      recognitionState.finishedAt = Date.now();
      if (recognitionState.running === 0) {
        recognitionState.progress = '';
      }
    });
}

// 登出时复位：同时让仍在跑的旧任务结果作废，避免串到下一个会话。
export function resetRecognition() {
  recognitionState.running = 0;
  recognitionState.progress = '';
  recognitionState.stage = '';
  recognitionState.history = [];
  recognitionState.summary = null;
  recognitionState.error = '';
  recognitionState.lastResult = null;
  recognitionState.targetDate = today();
  recognitionState.isIncremental = true;
  recognitionState.startedAt = 0;
  recognitionState.finishedAt = 0;
  recognitionState.runId++;
  recognitionState.lastResultId = recognitionState.runId;
}
