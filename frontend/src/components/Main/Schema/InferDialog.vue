<template>
  <el-dialog
    v-model="visible"
    title="从样例推断模板"
    width="760px"
    top="5vh"
    :close-on-click-modal="false"
  >
    <div class="space-y-4">
      <div class="grid grid-cols-2 gap-4">
        <div>
          <label class="block text-xs font-medium text-ink-2 mb-1">模板名称</label>
          <el-input v-model="name" placeholder="如：生鲜团购订单（留空自动生成）" maxlength="40" />
        </div>
        <div class="flex items-end gap-3">
          <div class="flex items-center gap-1">
            <el-switch v-model="useAI" />
            <span class="text-xs text-ink-2">用 AI 归纳字段</span>
          </div>
          <el-select v-if="useAI" v-model="provider" size="default" style="width: 150px">
            <el-option label="自动选择" value="" />
            <el-option label="DeepSeek" value="deepseek" />
            <el-option label="千问" value="qwen" />
          </el-select>
        </div>
      </div>

      <div>
        <label class="block text-xs font-medium text-ink-2 mb-1">
          样例消息 *（每条一段，至少 2 条更容易收敛字段；AI 失败自动退化为启发式）
        </label>
        <el-input
          v-model="samplesText"
          type="textarea"
          :rows="8"
          placeholder="11-7&#10;店名：示例广场店&#10;电话：13800138000&#10;地址：华夏省大兵市示例路1号&#10;【广香5斤】【黄柠檬3斤】"
        />
      </div>

      <div v-if="result" class="border border-line rounded-lg p-3 bg-surface">
        <p class="text-xs font-semibold text-brand-ink mb-2">
          推断结果（未保存）：{{ resultTemplate?.name }} · {{ resultTemplate?.fields?.length || 0 }} 个字段
        </p>
        <div class="flex flex-wrap gap-1 mb-2">
          <el-tag
            v-for="f in resultTemplate?.fields || []"
            :key="f.key"
            size="small"
            :type="f.required ? 'primary' : 'info'"
          >
            {{ f.label }}（{{ f.key }}）
          </el-tag>
        </div>
        <p class="text-[11px] text-ink-3">
          样例 {{ result?.report?.sample_count ?? 0 }} 条；
          字段候选 {{ result?.report?.proposals?.length ?? 0 }} 个；
          别名归并 {{ result?.report?.alias_merges?.length ?? 0 }} 处。
          可直接保存为 draft 模板，再在「模板管理」里微调后发布。
        </p>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <el-button @click="visible = false">关闭</el-button>
        <el-button :loading="inferring" @click="infer">推断</el-button>
        <el-button v-if="result" type="primary" :loading="saving" @click="saveAsTemplate">
          保存为模板（draft）
        </el-button>
      </div>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue';
import { ElMessage } from 'element-plus';
import { SchemaInferTemplate, SchemaSaveTemplate } from '../../../../wailsjs/go/main/App';

const props = defineProps<{ modelValue: boolean }>();
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void; (e: 'saved'): void }>();

const visible = computed({
  get: () => props.modelValue,
  set: (v) => emit('update:modelValue', v),
});

const name = ref('');
const samplesText = ref('');
const useAI = ref(true);
const provider = ref('');
const inferring = ref(false);
const saving = ref(false);
const result = ref<any>(null);
const resultTemplate = computed(() => result.value?.template);

watch(
  () => props.modelValue,
  (open) => {
    if (open) result.value = null;
  },
);

const infer = async () => {
  const samples = samplesText.value
    .split(/\n\s*\n/)
    .map((s) => s.trim())
    .filter(Boolean);
  if (samples.length === 0) {
    ElMessage.warning('请先粘贴至少一条样例消息');
    return;
  }
  inferring.value = true;
  try {
    const res: any = await SchemaInferTemplate({
      samples,
      id: '',
      name: name.value.trim(),
      useAi: useAI.value,
      provider: provider.value,
    });
    if (res?.error) {
      ElMessage.error(res.error);
      return;
    }
    result.value = res;
  } catch (e: any) {
    ElMessage.error(String(e));
  } finally {
    inferring.value = false;
  }
};

const saveAsTemplate = async () => {
  const t = resultTemplate.value;
  if (!t) return;
  saving.value = true;
  try {
    // Wails 生成的入参类型是带 convertValues() 的 model class，Go 侧 JSON
    // 反序列化只需数据字段；断言在编译期擦除，请求体不变。
    const res: any = await SchemaSaveTemplate({
      id: '',
      name: t.name,
      description: t.description || '由样例推断生成',
      fields: t.fields || [],
      items: t.items || { enabled: false },
      signatures: t.signatures || [],
    } as unknown as Parameters<typeof SchemaSaveTemplate>[0]);
    if (res?.error) {
      ElMessage.error(res.error);
      return;
    }
    ElMessage.success('模板已保存（draft），请在模板管理中检查字段后发布');
    visible.value = false;
    emit('saved');
  } catch (e: any) {
    ElMessage.error(String(e));
  } finally {
    saving.value = false;
  }
};
</script>
