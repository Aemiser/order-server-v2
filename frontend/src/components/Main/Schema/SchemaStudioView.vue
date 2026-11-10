<template>
  <div class="max-w-6xl mx-auto">
    <!-- 页头 -->
    <div class="flex items-start justify-between mb-4">
      <div>
        <h2 class="text-xl font-bold text-ink">模板工作台</h2>
        <p class="text-xs text-ink-3 mt-1">
          新架构纵切片：模板驱动的通用订单识别（orderschema + compose）。数据写入独立新表 orders_v2，
          与现有订单链路（数据预览区 / 账单结算）完全隔离，可随时对比验证。
        </p>
      </div>
      <el-button size="small" text @click="loadAll">刷新</el-button>
    </div>

    <!-- 分区切换 -->
    <div class="flex gap-1 mb-4 border-b border-line">
      <button
        v-for="p in panes"
        :key="p.key"
        class="px-4 py-2 text-sm rounded-t-md transition-colors"
        :class="activePane === p.key
          ? 'bg-brand-soft text-brand-ink font-medium border-b-2 border-brand'
          : 'text-ink-2 hover:text-ink'"
        @click="activePane = p.key"
      >
        {{ p.label }}
      </button>
    </div>

    <!-- ============ 模板管理 ============ -->
    <div v-if="activePane === 'templates'">
      <div class="flex items-center justify-between mb-3">
        <div class="flex items-center gap-3">
          <el-checkbox v-model="onlyPublished" label="只看已发布" @change="loadTemplates" />
          <span class="text-xs text-ink-3">识别路由只使用「已发布」的模板</span>
        </div>
        <div class="flex gap-2">
          <el-button @click="inferVisible = true">从样例推断</el-button>
          <el-button type="primary" @click="openEditor(null)">新建模板</el-button>
        </div>
      </div>

      <el-table :data="templates" v-loading="loadingTemplates" size="default" border>
        <el-table-column prop="template_id" label="ID" width="150" show-overflow-tooltip />
        <el-table-column prop="name" label="名称" min-width="140" />
        <el-table-column label="版本" width="70" align="center">
          <template #default="{ row }">v{{ row.version }}</template>
        </el-table-column>
        <el-table-column label="状态" width="100" align="center">
          <template #default="{ row }">
            <el-tag
              size="small"
              :type="row.status === 'published' ? 'success' : row.status === 'draft' ? 'warning' : 'info'"
            >
              {{ statusLabel(row.status) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="字段" width="70" align="center">
          <template #default="{ row }">{{ (row.fields || []).length }}</template>
        </el-table-column>
        <el-table-column label="签名关键词" min-width="180" show-overflow-tooltip>
          <template #default="{ row }">
            {{ (row.signatures || []).map((s: any) => (s.keywords || []).join(' / ')).join('；') }}
          </template>
        </el-table-column>
        <el-table-column label="操作" width="230" align="center">
          <template #default="{ row }">
            <el-button size="small" text type="primary" @click="openEditor(row)">编辑</el-button>
            <el-button
              v-if="row.status !== 'published'"
              size="small" text type="success" @click="publish(row)"
            >发布</el-button>
            <el-button
              v-if="row.status === 'published'"
              size="small" text type="warning" @click="deprecate(row)"
            >停用</el-button>
            <el-button size="small" text type="danger" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <div class="py-8 text-center text-ink-3 text-sm">
            还没有模板。点「从样例推断」粘贴几条订单消息，AI 会自动归纳字段与签名。
          </div>
        </template>
      </el-table>
    </div>

    <!-- ============ 识别运行 ============ -->
    <div v-if="activePane === 'run'" class="space-y-4">
      <div class="flex flex-wrap items-end gap-3 p-4 bg-surface border border-line rounded-lg">
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">开始日期</label>
          <el-date-picker v-model="runFrom" type="date" value-format="YYYY-MM-DD" style="width: 150px" />
        </div>
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">结束日期</label>
          <el-date-picker v-model="runTo" type="date" value-format="YYYY-MM-DD" style="width: 150px" />
        </div>
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">数据来源</label>
          <el-select v-model="runSource" style="width: 160px">
            <el-option v-for="s in sources" :key="s" :label="sourceLabel(s)" :value="s" />
          </el-select>
        </div>
        <div v-if="runSource === 'file'">
          <label class="block text-xs font-medium text-ink-2 mb-1">文件路径（CSV / JSONL）</label>
          <el-input v-model="runDataDir" clearable placeholder="D:/data/messages.jsonl" style="width: 260px" />
        </div>
        <div class="pb-1">
          <el-checkbox v-model="runDryRun" label="试跑（不入库）" />
        </div>
        <el-button type="primary" :loading="running" :disabled="running" @click="startRun">
          {{ running ? '识别中…' : runDryRun ? '开始试跑' : '开始识别' }}
        </el-button>
        <el-button v-if="running" @click="cancelRun">取消</el-button>
        <p class="w-full text-xs text-ink-3">
          按已发布模板路由 → 规则抽取 → {{ runDryRun ? '仅预览结果、不写 orders_v2' : '入库 orders_v2' }}。
          运行期不调用 AI，未配置 API Key 也能识别；AI 只在「从样例推断」时使用。
          文件来源读取文件内全部消息（忽略日期窗口），日期仅用于运行记录。
        </p>
      </div>

      <div v-if="running || progressText" class="text-sm text-brand-ink flex items-center gap-2">
        <span v-if="running" class="inline-block w-3 h-3 border-2 border-brand border-t-transparent rounded-full animate-spin" />
        {{ progressText }}
      </div>

      <!-- 上次运行结果 -->
      <template v-if="lastRun">
        <el-alert v-if="lastRun.error" :title="`识别失败：${lastRun.error}`" type="error" show-icon :closable="false" />
        <el-alert v-else-if="lastRun.cancelled" title="本轮识别已取消（未写入任何数据）" type="info" show-icon :closable="false" />
        <template v-else>
          <div class="flex gap-4 text-sm">
            <span class="px-3 py-1.5 bg-success/10 text-success rounded-md font-medium">
              {{ lastRun.dryRun ? '试跑抽取' : '识别入库' }} {{ lastRun.orderCount }} 笔
            </span>
            <span class="px-3 py-1.5 bg-warning/10 text-warning rounded-md font-medium">
              未匹配 {{ lastRun.unmatchedCount }} 条（需补模板或人工处理）
            </span>
          </div>

          <div>
            <h3 class="text-sm font-semibold text-ink mb-2">
              识别出的订单
              <span v-if="lastRun.dryRun" class="text-xs font-normal text-ink-3">（试跑结果未入库，仅供查看）</span>
            </h3>
            <OrderRows :orders="lastRun.orders || []" :readonly="!!lastRun.dryRun" @changed="onOrderChanged" />
          </div>

          <div v-if="(lastRun.unmatched || []).length">
            <h3 class="text-sm font-semibold text-ink mb-2">未匹配消息（前 50 条）</h3>
            <div class="border border-line rounded-lg divide-y divide-line max-h-72 overflow-y-auto custom-scrollbar">
              <div v-for="m in (lastRun.unmatched || []).slice(0, 50)" :key="m.ID" class="px-3 py-2">
                <p class="text-xs text-ink-3 mb-0.5">{{ m.Sender || '未知' }} · {{ m.Time }}</p>
                <p class="text-sm text-ink-2 whitespace-pre-wrap break-all">{{ m.Content }}</p>
              </div>
            </div>
          </div>
        </template>
      </template>
      <div v-else-if="!running" class="text-sm text-ink-3 p-8 text-center border border-dashed border-line rounded-lg">
        还没有运行记录。选好日期范围点「开始识别」。
      </div>
    </div>

    <!-- ============ 订单库 ============ -->
    <div v-if="activePane === 'orders'" class="space-y-3">
      <div class="flex flex-wrap items-end gap-3 p-4 bg-surface border border-line rounded-lg">
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">模板</label>
          <el-select v-model="orderFilter.templateId" clearable style="width: 180px" placeholder="全部模板">
            <el-option v-for="t in templates" :key="t.template_id" :label="t.name" :value="t.template_id" />
          </el-select>
        </div>
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">状态</label>
          <el-select v-model="orderFilter.status" clearable style="width: 140px" placeholder="全部状态">
            <el-option label="待审查" value="pending_review" />
            <el-option label="已确认" value="confirmed" />
            <el-option label="已丢弃" value="discarded" />
          </el-select>
        </div>
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">开始</label>
          <el-date-picker v-model="orderFilter.from" type="date" value-format="YYYY-MM-DD" style="width: 150px" />
        </div>
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">结束</label>
          <el-date-picker v-model="orderFilter.to" type="date" value-format="YYYY-MM-DD" style="width: 150px" />
        </div>
        <el-button type="primary" :loading="loadingOrders" @click="loadOrders">查询</el-button>
      </div>

      <OrderRows :orders="orders" :loading="loadingOrders" @changed="loadOrders" />
    </div>

    <!-- 对话框 -->
    <TemplateEditorDialog v-model="editorVisible" :template="editingTemplate" @saved="loadTemplates" />
    <InferDialog v-model="inferVisible" @saved="loadTemplates" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue';
import { ElMessage, ElMessageBox } from 'element-plus';
import { EventsOn } from '../../../../wailsjs/runtime/runtime';
import {
  SchemaListTemplates,
  SchemaPublishTemplate,
  SchemaDeprecateTemplate,
  SchemaDeleteTemplate,
  SchemaRunRecognition,
  SchemaCancelRun,
  SchemaGetRunStatus,
  SchemaListOrders,
  SchemaListSources,
} from '../../../../wailsjs/go/main/App';
import TemplateEditorDialog from './TemplateEditorDialog.vue';
import InferDialog from './InferDialog.vue';
import OrderRows from './OrderRows.vue';

const panes = [
  { key: 'templates', label: '模板管理' },
  { key: 'run', label: '识别运行' },
  { key: 'orders', label: '订单库' },
] as const;

const activePane = ref<'templates' | 'run' | 'orders'>('templates');

// ---- 模板 ----
const templates = ref<any[]>([]);
const loadingTemplates = ref(false);
const onlyPublished = ref(false);
const editorVisible = ref(false);
const editingTemplate = ref<any | null>(null);
const inferVisible = ref(false);

const statusLabel = (s: string) =>
  ({ published: '已发布', draft: '草稿', deprecated: '已停用' } as Record<string, string>)[s] || s;

const loadTemplates = async () => {
  loadingTemplates.value = true;
  try {
    const res: any = await SchemaListTemplates(onlyPublished.value);
    if (res?.error) {
      ElMessage.error(res.error);
      return;
    }
    templates.value = res?.templates || [];
  } catch (e: any) {
    ElMessage.error(String(e));
  } finally {
    loadingTemplates.value = false;
  }
};

const openEditor = (tpl: any) => {
  editingTemplate.value = tpl;
  editorVisible.value = true;
};

const publish = async (row: any) => {
  const res: any = await SchemaPublishTemplate(row.template_id);
  if (res?.error) return ElMessage.error(res.error);
  ElMessage.success(`已发布 ${row.name} v${res.template.version}`);
  loadTemplates();
};

const deprecate = async (row: any) => {
  const res: any = await SchemaDeprecateTemplate(row.template_id);
  if (res?.error) return ElMessage.error(res.error);
  ElMessage.success(`已停用 ${row.name}，识别路由不再命中`);
  loadTemplates();
};

const remove = async (row: any) => {
  try {
    await ElMessageBox.confirm(
      `将物理删除模板「${row.name}」的全部 ${row.version} 个版本（已识别订单不受影响）。确定？`,
      '删除模板',
      { type: 'warning' },
    );
  } catch {
    return;
  }
  const res: any = await SchemaDeleteTemplate(row.template_id);
  if (res?.error) return ElMessage.error(res.error);
  ElMessage.success('已删除');
  loadTemplates();
};

// ---- 识别运行 ----
const today = () => new Date().toISOString().slice(0, 10);
const runFrom = ref(today());
const runTo = ref(today());
const runSource = ref('wechat');
const runDataDir = ref('');
const runDryRun = ref(false);
const sources = ref<string[]>(['wechat', 'file']); // 绑定不可用时的回退列表
const running = ref(false);
const progressText = ref('');
const lastRun = ref<any>(null);

const sourceLabel = (s: string) =>
  ({ wechat: '微信群聊', file: 'CSV / JSONL 文件' } as Record<string, string>)[s] || s;

const loadSources = async () => {
  try {
    const res: any = await SchemaListSources();
    if (res?.error) return;
    if (Array.isArray(res?.sources) && res.sources.length) {
      sources.value = res.sources;
      if (!sources.value.includes(runSource.value)) runSource.value = sources.value[0];
    }
  } catch {
    /* 绑定不可用时保留内置列表 */
  }
};

const startRun = async () => {
  if (runSource.value === 'file' && !runDataDir.value.trim()) {
    return ElMessage.error('文件来源需要填写文件路径');
  }
  const res: any = await SchemaRunRecognition({
    from: runFrom.value,
    to: runTo.value,
    provider: '', // 运行期不调用 AI；provider 只用于「从样例推断」
    source: runSource.value,
    dataDir: runDataDir.value.trim(),
    dryRun: runDryRun.value,
  });
  if (res?.error) return ElMessage.error(res.error);
  running.value = true;
  progressText.value = '正在启动…';
};

const cancelRun = async () => {
  const res: any = await SchemaCancelRun();
  if (res?.error) return ElMessage.error(res.error);
  progressText.value = '正在取消…';
};

const onRunFinished = (report: any) => {
  running.value = false;
  progressText.value = '';
  lastRun.value = report;
  if (report?.error) {
    ElMessage.error(`识别失败：${report.error}`);
  } else if (report?.cancelled) {
    ElMessage.info('已取消本轮识别');
  } else if (report?.dryRun) {
    ElMessage.success(`试跑完成：抽取 ${report.orderCount} 笔（未入库），${report.unmatchedCount} 条未匹配`);
  } else {
    ElMessage.success(`识别完成：${report.orderCount} 笔订单入库，${report.unmatchedCount} 条未匹配`);
  }
};

const onOrderChanged = () => {
  // 订单状态变化后刷新识别结果里的行（简单起见直接重取状态）
  refreshRunStatus();
};

const refreshRunStatus = async () => {
  try {
    const res: any = await SchemaGetRunStatus();
    if (res?.error) return;
    running.value = !!res.running;
    if (res.lastRun) lastRun.value = res.lastRun;
  } catch {
    /* 静默 */
  }
};

// ---- 订单库 ----
const orderFilter = ref({ templateId: '', status: '', from: '', to: '' });
const orders = ref<any[]>([]);
const loadingOrders = ref(false);

const loadOrders = async () => {
  loadingOrders.value = true;
  try {
    const res: any = await SchemaListOrders({ ...orderFilter.value, limit: 200 });
    if (res?.error) {
      ElMessage.error(res.error);
      return;
    }
    orders.value = res?.orders || [];
  } catch (e: any) {
    ElMessage.error(String(e));
  } finally {
    loadingOrders.value = false;
  }
};

const loadAll = () => {
  loadTemplates();
  loadOrders();
  loadSources();
  refreshRunStatus();
};

// ---- 事件订阅（只用返回的取消函数，避免按名清监听误伤别人） ----
let unsubProgress: (() => void) | undefined;
let unsubResult: (() => void) | undefined;

onMounted(() => {
  loadTemplates();
  loadSources();
  refreshRunStatus();
  unsubProgress = EventsOn('schema-progress', (p: any) => {
    progressText.value = p?.text || '';
  });
  unsubResult = EventsOn('schema-result', (report: any) => {
    onRunFinished(report);
    loadOrders();
  });
});

onUnmounted(() => {
  unsubProgress?.();
  unsubResult?.();
});
</script>
