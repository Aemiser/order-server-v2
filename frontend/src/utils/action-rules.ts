// 「订单操作术语规则」的前端公共层：两个入口（人工审核区快捷按钮 / 应用设置页表格）
// 共用同一份选项、默认值与后端调用，避免两处各写一套映射。
import type { OrderActionRule } from '../types';

const bridge = () => (window as any)['go']['main']['App'];

export const ACTION_OPTIONS = [
  { value: 'delete', label: '删除订单', hint: '整笔订单取消 / 删除' },
  { value: 'modify', label: '修改订单', hint: '改地址、电话、店名、备注或规格' },
  { value: 'ignore', label: '明确排除', hint: '命中该词判为非操作，用于挡住疑问句等误伤' },
];

export const FIELD_OPTIONS = [
  { value: 'address', label: '收货地址' },
  { value: 'phone', label: '手机号' },
  { value: 'recipient', label: '店名 / 收件人' },
  { value: 'remarks', label: '备注（追加）' },
  { value: 'specs', label: '商品规格' },
];

export const VALUE_RULE_OPTIONS = [
  { value: 'none', label: '不需要新值' },
  { value: 'after', label: '取「为 / 改成 / ：」之后' },
  { value: 'phone', label: '抽消息里的手机号' },
  { value: 'append_rest', label: '剩余文本追加到备注' },
];

export const SCOPE_OPTIONS = [
  { value: 'both', label: '全部生效（推荐）' },
  { value: 'trigger', label: '只管候选筛选' },
  { value: 'prompt', label: '只管提示词与兜底' },
];

export const emptyActionRule = (): OrderActionRule => ({
  id: 0,
  keyword: '',
  action: 'delete',
  field: '',
  valueRule: 'none',
  scope: 'both',
  priority: 100,
  enabled: true,
  sortOrder: 0,
  remark: '',
});

export const actionLabel = (v: string) =>
  ACTION_OPTIONS.find(o => o.value === v)?.label || v || '—';

export const fieldLabel = (v: string) =>
  FIELD_OPTIONS.find(o => o.value === v)?.label || v || '—';

export const valueRuleLabel = (v: string) =>
  VALUE_RULE_OPTIONS.find(o => o.value === v)?.label || v || '—';

export const scopeLabel = (v: string) =>
  SCOPE_OPTIONS.find(o => o.value === v)?.label || v || '—';

export async function listActionRules(): Promise<OrderActionRule[]> {
  const rows = await bridge()['ListOrderActionRules']();
  return Array.isArray(rows) ? rows : [];
}

// 后端约定「空串 = 成功，非空 = 错误文案」。
export async function saveActionRule(rule: OrderActionRule): Promise<string> {
  const res = await bridge()['SaveOrderActionRule'](rule);
  return typeof res === 'string' ? res : '';
}

export async function deleteActionRule(id: number): Promise<string> {
  const res = await bridge()['DeleteOrderActionRule'](id);
  return typeof res === 'string' ? res : '';
}
