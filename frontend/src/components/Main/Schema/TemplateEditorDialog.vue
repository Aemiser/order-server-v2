<template>
  <el-dialog
    v-model="visible"
    :title="isEdit ? `编辑模板：${form.name}（另存为新版本）` : '新建模板'"
    width="860px"
    top="4vh"
    :close-on-click-modal="false"
  >
    <div class="space-y-4">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">模板名称 *</label>
          <el-input v-model="form.name" placeholder="如：生鲜团购订单" maxlength="40" />
        </div>
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">说明</label>
          <el-input v-model="form.description" placeholder="可选" maxlength="120" />
        </div>
      </div>

      <div>
        <label class="block text-xs font-medium text-ink-2 mb-1">
          路由签名关键词 *（每行一组；组内用逗号分隔多个关键词，命中越多分越高）
        </label>
        <el-input
          v-model="signatureText"
          type="textarea"
          :rows="2"
          placeholder="店名,电话,【&#10;收件人,地址"
        />
      </div>

      <div>
        <div class="flex items-center justify-between mb-1">
          <label class="block text-xs font-medium text-ink-2">字段定义 *</label>
          <el-button size="small" @click="addField">+ 添加字段</el-button>
        </div>
        <el-table :data="form.fields" size="small" border max-height="320">
          <el-table-column label="key *" width="120">
            <template #default="{ row }">
              <el-input v-model="row.key" size="small" placeholder="英文标识" />
            </template>
          </el-table-column>
          <el-table-column label="名称 *" width="110">
            <template #default="{ row }">
              <el-input v-model="row.label" size="small" />
            </template>
          </el-table-column>
          <el-table-column label="类型" width="120">
            <template #default="{ row }">
              <el-select v-model="row.type" size="small">
                <el-option v-for="t in fieldTypes" :key="t" :label="t" :value="t" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="抽取方式" width="110">
            <template #default="{ row }">
              <el-select v-model="row.extract.kind" size="small">
                <el-option label="标签" value="label" />
                <el-option label="正则" value="regex" />
                <el-option label="LLM" value="llm" />
              </el-select>
            </template>
          </el-table-column>
          <el-table-column label="标签/正则（逗号分隔）" min-width="180">
            <template #default="{ row }">
              <el-input
                v-if="row.extract.kind === 'label'"
                v-model="row.extractLabelsText"
                size="small"
                placeholder="店名,收件人"
              />
              <el-input
                v-else-if="row.extract.kind === 'regex'"
                v-model="row.extract.regex"
                size="small"
                placeholder="正则表达式"
              />
              <el-input v-else v-model="row.extract.hint" size="small" placeholder="给 AI 的提示" />
            </template>
          </el-table-column>
          <el-table-column label="必填" width="55" align="center">
            <template #default="{ row }">
              <el-checkbox v-model="row.required" size="small" />
            </template>
          </el-table-column>
          <el-table-column label="镜像列" width="65" align="center">
            <template #default="{ row }">
              <el-checkbox
                v-model="row.indexed"
                size="small"
                :disabled="!mirrorKeys.includes(row.key)"
              />
            </template>
          </el-table-column>
          <el-table-column label="" width="55" align="center">
            <template #default="{ $index }">
              <el-button size="small" text type="danger" @click="form.fields.splice($index, 1)">删</el-button>
            </template>
          </el-table-column>
        </el-table>
        <p class="text-[11px] text-ink-3 mt-1">
          镜像列仅对 recipient / phone / address / remarks 四个内置 key 生效：勾选后值会同步到订单表固定列，用于查询与去重。
        </p>
      </div>

      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">商品明细（行项目）</label>
          <el-switch v-model="form.items.enabled" active-text="启用【商品】解析" />
        </div>
        <div v-if="form.items.enabled">
          <label class="block text-xs font-medium text-ink-2 mb-1">品名标签（逗号分隔）</label>
          <el-input v-model="itemNameText" placeholder="默认自动识别【】内容" />
        </div>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <el-button @click="visible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存（draft）</el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { ElMessage } from 'element-plus';
import { SchemaSaveTemplate } from '../../../../wailsjs/go/main/App';

const props = defineProps<{ modelValue: boolean; template: any | null }>();
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'saved'): void }>();

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
});
const isEdit = computed(() => !!props.template?.template_id);

const fieldTypes = ['string', 'number', 'phone', 'date', 'bool', 'enum', 'measure', 'array', 'object'];
const mirrorKeys = ['recipient', 'phone', 'address', 'remarks'];

interface FieldRow {
  key: string;
  label: string;
  type: string;
  required: boolean;
  indexed: boolean;
  aliases: string[];
  extract: { kind: string; labels?: string[]; regex?: string; hint?: string };
  extractLabelsText: string;
}

const emptyForm = () => ({
  name: '',
  description: '',
  fields: [] as FieldRow[],
  items: { enabled: false, name_labels: [] as string[], quantity_unit: '', allow_attrs: true },
});

const form = ref<any>(emptyForm());
const signatureText = ref('');
const itemNameText = ref('');
const saving = ref(false);

watch(
  () => props.modelValue,
  (open) => {
    if (!open) return;
    const t = props.template;
    if (t) {
      form.value = {
        name: t.name,
        description: t.description || '',
        fields: (t.fields || []).map((f: any) => ({
          key: f.key,
          label: f.label,
          type: f.type,
          required: !!f.required,
          indexed: !!f.indexed,
          aliases: f.aliases || [],
          extract: { ...(f.extract || { kind: 'label' }) },
          extractLabelsText: (f.extract?.labels || []).join(','),
        })),
        items: {
          enabled: !!t.items?.enabled,
          name_labels: t.items?.name_labels || [],
          quantity_unit: t.items?.quantity_unit || '',
          allow_attrs: t.items?.allow_attrs ?? true,
        },
      };
      signatureText.value = (t.signatures || []).map((s: any) => (s.keywords || []).join(',')).join('\n');
    } else {
      form.value = emptyForm();
      signatureText.value = '';
    }
    itemNameText.value = form.value.items.name_labels.join(',');
  },
);

const addField = () => {
  form.value.fields.push({
    key: '',
    label: '',
    type: 'string',
    required: false,
    indexed: false,
    aliases: [],
    extract: { kind: 'label', labels: [] },
    extractLabelsText: '',
  });
};

const save = async () => {
  const fields = form.value.fields
    .filter((f: FieldRow) => f.key.trim() && f.label.trim())
    .map((f: FieldRow) => ({
      key: f.key.trim(),
      label: f.label.trim(),
      type: f.type,
      required: f.required,
      indexed: f.indexed && mirrorKeys.includes(f.key.trim()),
      aliases: f.aliases,
      extract: {
        kind: f.extract.kind,
        labels: f.extract.kind === 'label'
          ? f.extractLabelsText.split(/[,，]/).map((s) => s.trim()).filter(Boolean)
          : undefined,
        regex: f.extract.kind === 'regex' ? f.extract.regex : undefined,
        hint: f.extract.kind === 'llm' ? f.extract.hint : undefined,
      },
    }));
  const signatures = signatureText.value
    .split('\n')
    .map((line) => line.split(/[,，]/).map((s) => s.trim()).filter(Boolean))
    .filter((kw) => kw.length > 0)
    .map((keywords) => ({ keywords }));

  const items = {
    enabled: form.value.items.enabled,
    name_labels: itemNameText.value.split(/[,，]/).map((s) => s.trim()).filter(Boolean),
    quantity_unit: form.value.items.quantity_unit,
    allow_attrs: form.value.items.allow_attrs,
  };

  saving.value = true;
  try {
    // Wails 生成的入参类型是带 convertValues() 的 model class，Go 侧 JSON
    // 反序列化只需数据字段；断言在编译期擦除，请求体不变。
    const res: any = await SchemaSaveTemplate({
      id: props.template?.template_id || '',
      name: form.value.name.trim(),
      description: form.value.description.trim(),
      fields,
      items,
      signatures,
    } as unknown as Parameters<typeof SchemaSaveTemplate>[0]);
    if (res?.error) {
      ElMessage.error(res.error);
      return;
    }
    ElMessage.success(isEdit.value ? '已保存为新版本（draft）' : '模板已创建（draft）');
    visible.value = false;
    emit('saved');
  } catch (e: any) {
    ElMessage.error(String(e));
  } finally {
    saving.value = false;
  }
};
</script>
