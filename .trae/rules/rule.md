# OrderMaster Pro - 订单管理系统设计规范

## 项目概述
现代简约深色主题的桌面应用订单管理系统，采用侧边栏导航布局，包含订单输入、详情管理和应用设置三个页面。

## 色彩体系
- **背景深色**: #0F1115 (主背景)
- **侧边栏背景**: #16191E
- **卡片背景**: #1E2228
- **主色调 (蓝)**: #3B82F6 (按钮、高亮)
- **辅助色 (青绿)**: #14B8A6 (次级强调)
- **文本柔和灰**: #94A3B8 (muted text)
- **玻璃态背景**: rgba(255,255,255,0.03) 配合边框 rgba(255,255,255,0.1)

## 排版系统
- **展示字体**: Cabinet Grotesk (800 weight) - 标题、菜单
- **正文字体**: Satoshi (400, 500, 700 weights) - 内容、按钮
- **代码字体**: 等宽字体用于电话号码、路径等

## Tailwind 配置
- 深色背景: bg-[#0F1115]
- 侧边栏: bg-[#16191E]
- 卡片: bg-[#16191E]
- 浅色边框: border-white/5
- 文本: text-slate-200, text-slate-400, text-slate-500

## 核心组件

### 1. Sidebar 导航组件 (componentId: c789e483-4c54-42b7-846b-b007784e2f49)
左侧固定导航栏，包含：
- Logo + 应用名称 (OrderMaster)
- 三项导航菜单 (订单输入、订单详情、应用设置)
- 系统状态指示卡 (绿色脉冲点表示就绪)
- 退出登录按钮

**Props**:
- activeItem: 'input' | 'details' | 'settings' (高亮当前页)
- inputHref: 订单输入页链接
- detailsHref: 订单详情页链接
- settingsHref: 应用设置页链接
- isSystemReady: 系统状态布尔值
- statusMessage: 状态文本

### 2. Header 顶部组件 (componentId: 11bec3a5-bcf8-4be6-a983-53e2c5370d3e)
固定顶部信息栏，包含：
- 面包屑导航 (工作区 > 当前页)
- 通知铃铛 (带红点指示)
- 用户信息卡 (用户名、角色、头像)

**Props**:
- workspaceHref: 工作区链接
- userName: 用户名显示
- userRole: 用户角色标签
- showNotification: 是否显示红点

## 页面架构

### 页面 1: 订单输入 (e2efe3d0-318f-4857-b5fb-000e5f140345)
**功能**: 输入原始订单文本，AI自动识别生成表格
- 大文本区域 (支持粘贴、拖入)
- 一键识别按钮 (带sparkles图标)
- 数据预览表格 (6列数据)
- 确认和导出按钮

### 页面 2: 订单详情 (23bcaab9-1c68-47ba-b486-91b11bd093c3)
**功能**: 管理和查询所有订单数据
- 查询区域 (客户名、时间范围、订单状态)
- 完整数据表格 (7列: 姓名、电话、地址、商品、数量、创建时间、操作)
- 编辑/删除按钮
- 分页控制 (支持128条记录)
- 批量导出功能

### 页面 3: 应用设置 (1d33724f-ecd6-4dd9-80d9-225fb683bc8b)
**功能**: 配置系统参数和用户偏好
- 导出路径设置 (显示+修改)
- AI识别引擎选择 (3款模型)
- 自动保存间隔 (15/30/60分钟)
- 导出格式选择 (XLSX/CSV)
- 主题模式 (浅色/深色/跟随系统)
- 保存所有配置按钮

## 交互细节

### 玻璃态输入框 (glass-input)
```css
.glass-input {
  background-color: rgba(255,255,255,0.03);
  border: 1px solid rgba(255,255,255,0.1);
  transition: all 0.2s ease;
}
.glass-input:focus {
  outline: none;
  border-color: var(--accent-blue);
  background-color: rgba(255,255,255,0.06);
}
```

### 活跃导航样式 (active-nav)
- 背景: rgba(59,130,246,0.1)
- 文字: #3B82F6 (蓝色)
- 右边框: 3px solid #3B82F6

### 按钮样式
- **主按钮**: bg-blue-600 + shadow-lg shadow-blue-600/20
- **辅助按钮**: bg-white/5 border border-white/10
- **删除按钮**: text-red-400/60 hover:text-red-400 hover:bg-red-400/10

### 表格交互
- 行悬停: background-color: rgba(255,255,255,0.02)
- 标签: 彩色背景 + 文字 (蓝/青/紫)

## 导航链接配置

| 页面 | Draft ID | URL |
|-----|----------|-----|
| 订单输入 | e2efe3d0-318f-4857-b5fb-000e5f140345 | https://p.superdesign.dev/draft/e2efe3d0-318f-4857-b5fb-000e5f140345 |
| 订单详情 | 23bcaab9-1c68-47ba-b486-91b11bd093c3 | https://p.superdesign.dev/draft/23bcaab9-1c68-47ba-b486-91b11bd093c3 |
| 应用设置 | 1d33724f-ecd6-4dd9-80d9-225fb683bc8b | https://p.superdesign.dev/draft/1d33724f-ecd6-4dd9-80d9-225fb683bc8b |

## 动画效果
- **过渡**: 所有交互元素使用 transition-all duration-200
- **脉冲**: 系统状态点使用 animate-pulse
- **悬停**: 按钮和链接有色彩变化效果

## 响应式设计
- 桌面优先设计 (1440px 宽度)
- 使用 Tailwind 响应式类 (md: lg:)
- 侧边栏固定宽度 (w-64)
- 主内容区灵活布局 (flex-1)

---
**最后更新**: 2024年 | **版本**: 1.0 | **框架**: Tailwind CSS + Iconify