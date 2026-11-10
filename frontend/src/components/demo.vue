<template>
  <div class="table-card">
    <div class="table-wrapper">
      <table>
        <thead>
          <tr class="product-group">
            <th>日期 / 产品</th>
            <th colspan="2">香水柠檬</th>
            <th colspan="2">广香一级</th>
            <th colspan="2">广香二级</th>
            <th colspan="2">黄柠檬</th>
            <th colspan="2">金桔</th>
          </tr>
          <tr class="spec-header">
            <th></th>
            <th>5斤</th>
            <th>8斤</th>
            <th>10斤</th>
            <th>20斤</th>
            <th>7斤</th>
            <th>15斤</th>
            <th>3斤</th>
            <th>6斤</th>
            <th>2斤</th>
            <th>4斤</th>
          </tr>
        </thead>
        <tbody>
          <!-- 使用v-for动态渲染数据行 -->
          <tr v-for="(row, index) in tableData" :key="index" class="data-row">
            <td class="date-column">{{ row.date }}</td>
            <td v-for="(value, idx) in row.values" :key="idx" class="spec-cell">
              <template v-if="value !== '—'">
                <span class="spec-value">{{ value }}</span>
                <span class="spec-unit">件</span>
              </template>
              <template v-else>
                <span class="empty-cell">—</span>
              </template>
            </td>
          </tr>

          <!-- 总计行 -->
          <tr class="total-row">
            <td class="total-label">总计 (件)</td>
            <td>447</td>
            <td>31</td>
            <td>168</td>
            <td>168</td>
            <td>1.6</td>
            <td>—</td>
            <td>34</td>
            <td>4.4</td>
            <td>168</td>
            <td>1.6</td>
          </tr>

          <!-- 单价行 -->
          <tr class="price-row">
            <td class="price-label">单价 (元/件)</td>
            <td>34</td>
            <td>40.8</td>
            <td>54.4</td>
            <td>16</td>
            <td>8</td>
            <td>—</td>
            <td>16</td>
            <td>8</td>
            <td>16</td>
            <td>8</td>
          </tr>

          <!-- 总金额行 -->
          <tr class="amount-row">
            <td class="amount-label">总金额 (元)</td>
            <td>15198</td>
            <td>122.4</td>
            <td>54.4</td>
            <td>25168</td>
            <td>1.6</td>
            <td>—</td>
            <td>544</td>
            <td>35.2</td>
            <td>2688</td>
            <td>12.8</td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- 底部说明 -->
    <div class="legend-section">
      <div class="legend-item">
        <span class="legend-color legend-blue"></span>
        <span>香水柠檬: 5斤装(34元/件) · 8斤装(40.8元/件)</span>
      </div>
      <div class="legend-item">
        <span class="legend-color legend-teal"></span>
        <span>广香一级: 10斤装(54.4元/件) · 20斤装(16元/件)</span>
      </div>
      <div class="legend-item">
        <span class="legend-color legend-purple"></span>
        <span>金桔: 2斤装(16元/件) · 4斤装(8元/件)</span>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'

// 表格数据
const tableData = ref([
  { date: '3月8日', values: [17, 4, 11, 12, '—', '—', 5, '—', 11, '—'] },
  { date: '3月9日', values: [11, 3, 8, 7, 2, '—', 4, 1, 8, 2] },
  { date: '3月10日', values: [30, 3, 11, 11, 2, '—', 4, '—', 12, '—'] },
  { date: '3月11日', values: [24, 7, 7, 11, 8, '—', 5, 1, 11, '—'] },
  { date: '3月12日', values: [17, 0, 9, 11, 2, '—', 3, '—', 12, 3] },
  { date: '3月13日', values: [7, 8, 5, 11, '—', 2, 4, '—', 8, '—'] },
  { date: '3月14日', values: [18, 1, 11, 11, '—', '—', 1, '—', 11, 1] },
  { date: '3月15日', values: [21, 1, 11, 11, '—', '—', 1, '—', 11, '—'] },
  { date: '3月16日', values: [23, 1, 11, 11, '—', '—', 1, '—', 11, '—'] },
  { date: '3月17日', values: [17, 1, 11, 11, '—', '—', 1, '—', 11, '—'] },
  { date: '3月18日', values: [20, 0, 3, 7, 1, 1, '—', '—', 11, '—'] }
])
</script>

<style scoped>
/* 设计变量（浅色，复用全局令牌） */
:root {
  --bg-card: var(--color-surface);
  --accent-blue: var(--color-brand);
  --accent-teal: #14b8a6;
  --accent-purple: #8b5cf6;
  --text-primary: var(--color-ink);
  --text-secondary: var(--color-ink-2);
  --text-muted: var(--color-ink-3);
  --border-light: var(--color-line);
  --border-lighter: var(--color-line-subtle);
  --row-hover: var(--color-hover);
}

* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

/* 表格卡片 */
.table-card {
  background-color: var(--color-surface);
  border: 1px solid var(--color-line);
  border-radius: var(--radius-lg);
  padding: 24px 20px;
  box-shadow: var(--shadow-card);
  margin: 20px;
}

.table-wrapper {
  border-radius: var(--radius-md);
  border: 1px solid var(--color-line);
  overflow: auto;
  background-color: var(--color-surface);
}

table {
  width: 100%;
  border-collapse: collapse;
  min-width: 1000px;
  font-size: 0.95rem;
}

th {
  background: var(--color-subtle);
  color: var(--color-ink-2);
  font-weight: 600;
  font-size: 0.95rem;
  padding: 18px 12px;
  white-space: nowrap;
  border-bottom: 2px solid var(--color-line);
  text-align: center;
}

.product-group th {
  background: var(--color-subtle);
  color: var(--color-ink-2);
  font-size: 1rem;
  padding: 14px 8px;
}

.spec-header th {
  background: var(--color-subtle);
  color: var(--color-ink-3);
}

td {
  padding: 14px 12px;
  border-bottom: 1px solid var(--color-line-subtle);
  text-align: center;
  vertical-align: middle;
  color: var(--color-ink);
}

.data-row:hover td {
  background-color: var(--color-hover);
}

.date-column {
  background: var(--color-subtle);
  font-weight: 600;
  color: var(--color-ink-2);
  text-align: left;
  padding-left: 18px;
  font-size: 1rem;
  border-right: 1px solid var(--color-line);
  position: sticky;
  left: 0;
  background-color: var(--color-surface);
}

.spec-cell {
  font-weight: 500;
  color: var(--color-ink);
}

.spec-value {
  font-weight: 600;
  color: var(--color-ink);
  font-size: 1.1rem;
}

.spec-unit {
  font-size: 0.75rem;
  color: var(--color-ink-3);
  margin-left: 2px;
  font-weight: 400;
}

.empty-cell {
  color: var(--color-ink-3);
  font-size: 0.9rem;
}

/* 总计行 */
.total-row {
  background: var(--color-subtle);
}

.total-row td {
  background: var(--color-subtle);
  border-top: 2px solid var(--color-line);
  border-bottom: 2px solid var(--color-line);
  font-weight: 600;
  color: var(--color-ink-2);
}

.total-label {
  background: var(--color-brand-soft);
  font-weight: 700;
  color: var(--color-brand-ink);
  text-align: left;
  padding-left: 18px;
}

/* 单价行 */
.price-row {
  background: var(--color-subtle);
}

.price-row td {
  background: var(--color-subtle);
  color: var(--color-ink-2);
  border-bottom: 1px dashed var(--color-line);
}

.price-label {
  font-weight: 600;
  color: var(--color-ink-2);
  text-align: left;
  padding-left: 18px;
}

/* 总金额行 */
.amount-row {
  background: var(--color-brand-soft);
}

.amount-row td {
  background: var(--color-brand-soft);
  border-top: 2px solid var(--color-line);
  font-weight: 600;
  color: var(--color-brand-ink);
}

.amount-label {
  font-weight: 700;
  color: var(--color-brand-ink);
  text-align: left;
  padding-left: 18px;
}

/* 底部图例 */
.legend-section {
  margin-top: 20px;
  padding: 0 12px;
  display: flex;
  gap: 24px;
  flex-wrap: wrap;
  color: var(--color-ink-2);
  font-size: 0.9rem;
}

.legend-item {
  display: flex;
  align-items: center;
  gap: 8px;
}

.legend-color {
  width: 10px;
  height: 10px;
  border-radius: 4px;
}
.legend-blue { background: color-mix(in srgb, var(--accent-blue) 20%, transparent); }
.legend-teal { background: color-mix(in srgb, var(--accent-teal) 20%, transparent); }
.legend-purple { background: color-mix(in srgb, var(--accent-purple) 20%, transparent); }

/* 响应式 */
@media (max-width: 768px) {
  .table-card {
    margin: 10px;
    padding: 16px 12px;
  }
}
</style>