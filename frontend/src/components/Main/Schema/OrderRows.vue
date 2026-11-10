<template>
  <el-table :data="orders" v-loading="loading" size="small" border max-height="480">
    <el-table-column prop="id" label="#" width="70" align="center" />
    <el-table-column label="收件人" min-width="110" show-overflow-tooltip>
      <template #default="{ row }">{{ row.recipient_name || fieldText(row, 'recipient') || '—' }}</template>
    </el-table-column>
    <el-table-column label="电话" width="125">
      <template #default="{ row }">{{ row.phone || fieldText(row, 'phone') || '—' }}</template>
    </el-table-column>
    <el-table-column label="地址" min-width="200" show-overflow-tooltip>
      <template #default="{ row }">{{ row.address || fieldText(row, 'address') || '—' }}</template>
    </el-table-column>
    <el-table-column label="商品明细" min-width="170" show-overflow-tooltip>
      <template #default="{ row }">
        <span v-if="(row.items || []).length">
          {{ row.items.map((i: any) => `${i.name}${i.qty?.value ?? ''}${i.qty?.unit || ''}`).join('，') }}
        </span>
        <span v-else class="text-ink-3">—</span>
      </template>
    </el-table-column>
    <el-table-column label="备注" min-width="110" show-overflow-tooltip>
      <template #default="{ row }">{{ row.remarks || fieldText(row, 'remarks') || '—' }}</template>
    </el-table-column>
    <el-table-column label="模板" width="120" show-overflow-tooltip>
      <template #default="{ row }">{{ row.template_id }} v{{ row.template_version }}</template>
    </el-table-column>
    <el-table-column label="状态" width="90" align="center">
      <template #default="{ row }">
        <el-tag size="small" :type="statusType(row.status)">{{ statusText(row.status) }}</el-tag>
      </template>
    </el-table-column>
    <el-table-column label="原文" width="70" align="center">
      <template #default="{ row }">
        <el-popover placement="left" :width="380" trigger="click">
          <template #reference>
            <el-button size="small" text type="primary">查看</el-button>
          </template>
          <p class="text-xs text-ink-3 mb-2">
            {{ row.source }} · {{ fmtTime(row.order_time) }} · 消息ID {{ row.source_msg_id }}
          </p>
          <p class="text-sm text-ink whitespace-pre-wrap break-all max-h-72 overflow-y-auto">{{ row.raw_text }}</p>
        </el-popover>
      </template>
    </el-table-column>
    <el-table-column v-if="!readonly" label="操作" width="170" align="center">
      <template #default="{ row }">
        <el-button
          v-if="row.status !== 'confirmed'"
          size="small" text type="success" @click="setStatus(row, 'confirmed')"
        >确认</el-button>
        <el-button
          v-if="row.status !== 'discarded'"
          size="small" text type="warning" @click="setStatus(row, 'discarded')"
        >丢弃</el-button>
        <el-button size="small" text type="danger" @click="del(row)">删</el-button>
      </template>
    </el-table-column>
    <template #empty>
      <div class="py-6 text-ink-3 text-sm">暂无订单</div>
    </template>
  </el-table>
</template>

<script setup lang="ts">
import { ElMessage, ElMessageBox } from 'element-plus';
import { SchemaSetOrderStatus, SchemaDeleteOrder } from '../../../../wailsjs/go/main/App';

defineProps<{ orders: any[]; loading?: boolean; readonly?: boolean }>();
const emit = defineEmits<{ (e: 'changed'): void }>();

const fieldText = (row: any, key: string) => {
  const v = row.fields?.[key]?.value;
  return v === undefined || v === null ? '' : String(v);
};

const statusText = (s: string) =>
  ({ pending_review: '待审查', confirmed: '已确认', discarded: '已丢弃' } as Record<string, string>)[s] || s;
const statusType = (s: string) =>
  ({ pending_review: 'warning', confirmed: 'success', discarded: 'info' } as Record<string, string>)[s] || 'info';

const fmtTime = (t: string) => (t ? String(t).replace('T', ' ').slice(0, 16) : '');

const setStatus = async (row: any, status: string) => {
  const res: any = await SchemaSetOrderStatus(row.id, status);
  if (res?.error) return ElMessage.error(res.error);
  row.status = status;
  emit('changed');
};

const del = async (row: any) => {
  try {
    await ElMessageBox.confirm(`确定删除订单 #${row.id}？（${row.recipient_name || '未知收件人'}）`, '删除订单', {
      type: 'warning',
    });
  } catch {
    return;
  }
  const res: any = await SchemaDeleteOrder(row.id);
  if (res?.error) return ElMessage.error(res.error);
  ElMessage.success('已删除');
  emit('changed');
};
</script>
