# OrderMaster Pro - 智能订单管理系统 (本地版)

这是一个基于 Vue 3 + TypeScript + Tailwind CSS 构建的订单管理系统。

## 功能特点
- **订单解析**：支持从文本中快速提取订单信息（姓名、电话、地址、商品）。
- **数据预览**：实时表格展示解析结果。
- **Excel 导出**：一键导出为标准的 `.xlsx` 文件。
- **现代化 UI**：深色系豪华仪表盘设计。

## 本地运行步骤

### 1. 安装依赖
```bash
npm install
```

### 2. 启动开发服务器
```bash
npm run dev
```
启动后访问 `http://localhost:5173`。

### 3. 构建生产版本
```bash
npm run build
```

## 项目结构
- `src/App.vue`: 主应用组件，包含所有逻辑和样式。
- `src/main.ts`: 应用入口文件。
- `src/index.css`: 全局样式及 Tailwind 配置。
- `vite.config.ts`: Vite 配置文件。

## 注意事项
- 本版本已移除 Gemini AI 依赖，使用本地正则解析逻辑。
- 解析格式示例：`张三 13800138000 北京市朝阳区XX路 苹果x1`
