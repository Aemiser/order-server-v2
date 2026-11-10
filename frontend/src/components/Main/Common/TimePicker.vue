<template>
  <div class="time-picker-container">
    <label v-if="label" class="text-[10px] font-bold text-ink-3 uppercase tracking-[0.1em] flex items-center gap-2 mb-2">
      {{ label }}
    </label>
    <div class="relative">
      <ElDatePicker
        v-model="localValue"
        :type="type"
        :range-separator="rangeSeparator"
        :start-placeholder="startPlaceholder"
        :end-placeholder="endPlaceholder"
        class="w-full"
        :popper-class="'date-picker-popper'"
        :disabled="disabled"
        @change="handleChange"
      />
    </div>
    <div v-if="error" class="mt-2">
      <p class="text-danger text-xs font-medium">{{ error }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, watch, computed } from 'vue';
import { ElDatePicker } from 'element-plus';

// 定义组件属性
const props = defineProps<{
  modelValue: string | [string, string] | null;
  label?: string;
  type?: 'date' | 'daterange' | 'datetime' | 'datetimerange' | 'month' | 'monthrange' | 'year';
  rangeSeparator?: string;
  startPlaceholder?: string;
  endPlaceholder?: string;
  disabled?: boolean;
  error?: string;
}>();

// 定义组件事件
const emit = defineEmits<{
  (e: 'update:modelValue', value: string | [string, string] | null): void;
  (e: 'change', value: string | [string, string] | null): void;
}>();

// 本地状态
const localValue = ref<string | [string, string] | null>(props.modelValue);

// 监听外部值变化
watch(() => props.modelValue, (newValue) => {
  localValue.value = newValue;
});

// 处理值变化
const handleChange = (value: any) => {
  localValue.value = value;
  emit('update:modelValue', value);
  emit('change', value);
};

// 计算属性
const rangeSeparator = computed(() => props.rangeSeparator || '至');
const startPlaceholder = computed(() => props.startPlaceholder || '开始日期');
const endPlaceholder = computed(() => props.endPlaceholder || '结束日期');
const type = computed(() => props.type || 'date');
</script>

<style scoped>
.time-picker-container {
  width: 100%;
}
/* Element Plus 浅色主题已在 src/index.css 全局处理 */
</style>
