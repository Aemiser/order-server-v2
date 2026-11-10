# 贡献指南（Contributing）

欢迎参与 OnePoint / order_server 后端。本仓库正在向「模块化、接口驱动、方便第三方扩展」的架构演进，
因此**扩展能力是第一等公民**：新增一个数据源、AI 提供商或订单模板，应当**只新增一个包 + 一处注册**，
**不修改任何既有文件**。

## 一、架构约定（必读）
- 依赖方向严格单向：`delivery(Wails/CLI/Web)` → `ports(接口)` → `orderschema(领域内核)` → `kernel(零业务依赖基建)`。
- **不要**在业务代码里写死具体实现、用 `switch` 分发可替换的能力；请面向 `go/ports` 里的接口编程。
- **不要**在领域层 import Wails runtime、或直接使用 `go/global` 的包级全局；依赖通过构造函数注入。
- 命名空间 / 模块配置走 `ports.ConfigProvider`，不要写死表名或列名。

## 二、如何新增一个「数据源」（ChatSource）
1. 新建包 `go/sources/<your-source>/<your-source>.go`，实现 `ports.ChatSource`：
   ```go
   type Source struct { /* 你的配置 */ }
   func (s *Source) Name() string { return "your-source" }
   func (s *Source) Export(ctx context.Context, opt ports.ExportOptions) (*ports.ExportResult, error) {
       // 读取你的数据源，归一化为 []ports.ChatMessage
   }
   ```
2. **接进模板工作台**（推荐）：在自己的 `init()` 里注册即可，不必改任何现有文件：
   ```go
   func init() {
       schemaservice.MustRegisterSource("your-source", func(opt schemaservice.RunOptions) (ports.ChatSource, error) {
           return New(/* 从 opt 读取路径等参数 */), nil
       })
   }
   ```
   注册名大小写不敏感，重复注册 / 空名 / 空工厂会在启动期报错；前端「数据来源」下拉由 `SchemaListSources` 自动拉取。
   在 CLI / 示例里也可以直接用 `compose.NewRecognitionEngine(src, store, logger, threshold)` 注入。
3. **运行期不依赖 AI**：识别链路只做「导出 → 按已发布模板路由 → 确定性抽取」，没有 AI Key 也必须能跑；AI 只用于设计期的样例归纳。别在你的 Source 里反过来要求 AI 配置。
4. 参考实现：`go/sources/csv`（CSV/JSONL，无外部依赖）、`go/sources/wechat`（微信唯一实现，默认构建也编译）、`go/schemaservice/sources.go`（注册表与内置来源）。想接微信数据源直接用 `wechatsource.New(opts)`，不要在业务包里再写一份适配器。

## 三、如何新增一个「AI 提供商」（CompletionProvider）
1. 新建包实现 `ports.CompletionProvider`（OpenAI 兼容的请求/响应结构已定义好），或直接用 `ai.NewProvider(ai.ProviderConfig{...})` 配置一个兼容端点，`ai.Register(...)` 注册后按名字取用。
2. 注入到设计期引擎 `compose.NewInferenceEngine(ai, store, logger)`，或完整引擎 `NewEngine(chat, ai, store, logger, threshold)`。**运行期识别走 `NewRecognitionEngine`，不需要 AI**——不要把 API Key 变成识别的前置条件。
3. 重试 / 超时 / 限流 / 成本统计请内聚在你的 Provider 里——**AI 模块不认识「订单」二字**。`Complete` 收到的 `ctx` 必须一路传给底层 HTTP 请求（`http.NewRequestWithContext`），取消要能立即中断在途调用，不要在请求返回后才检查 `ctx.Done()`。
4. 主备高可用：用 `ai.NewFallback(primary, backup)` 包装多个 provider，对上层完全透明。注意它对**流式请求不降级**（避免用户看到重复文本）、ctx 取消后不再尝试备用端点。
5. 参考：`go/ai`（OpenAI 兼容 Provider + 注册表 + 降级组合器）、`go/utils/httputils`（`AiRequestCtx` / `AiStreamRequestCtx`，带状态码校验与取消语义；旧入口保持原有行为）。

## 四、如何新增一个「订单模板」
- 模板与订单持久化的契约在 `go/orderschema.Store`（模板 + 订单二合一，自带 MemoryStore/GormStore 与版本化迁移）。
- 用 `orderschema.InferTemplate`（启发式 + LLM 归纳）从样例消息生成模板，或手工构造 `orderschema.Template` 后 `store.SaveTemplate` 发布。
- 路由（`Route`/`RouteBest`）按模板签名自动匹配消息，无需你写分发逻辑。

## 五、构建标签（build tags）
- 默认构建：`go build ./...` —— **不链入** `go/wechatlog`（隔离边界是 `go/wechatapi` 门面），微信相关入口返回明确的降级提示，其余功能照常。
- 需要微信能力时：`go build -tags wechat ./...`；桌面应用由 `wails.json` 的 `build:tags` 自动带上，`wails build` / `wails dev` 无需手动加参数。
- `go/sources/wechat` 是微信 ChatSource 的**唯一**实现，**不带 build tag**，默认构建也参与编译与测试（CI 会断言其依赖图不含 `go/wechatlog`）。它只依赖 `go/wechatapi` 门面；合规敏感的取密钥 / 解密代码只留在 `go/wechatapi/enabled.go`（`//go:build wechat`）。新增同类能力请照此把直接依赖收进带标签的门面实现，**不要在主程序或上层适配器里直接 import**。
- Windows 专用代码请加 `//go:build windows`，保证其他平台 `go build ./...` 不受影响。

## 六、测试与质量门禁
- 每个新包**必须**带单元测试，且能在默认构建下编译通过（不要把跨平台/合规敏感代码塞进默认构建）。
- 提交前请跑：
  ```bash
  go build ./...
  go vet ./...
  go test ./...
  cd frontend && npm run lint && npm run build
  ```
- **前端类型门禁**：`npm run lint`（`vue-tsc --noEmit --skipLibCheck`）必须零错误，CI 的 `frontend` job 会挡住新增类型债务。`frontend/wailsjs/` 是 Wails 生成目录，**不要手改**：生成的 model class 带 `convertValues()` 等附加成员（有的还有 `createdAt`），而 Go 侧 JSON 反序列化只需数据字段。业务侧传纯数据对象时，在调用点用 `as unknown as Parameters<typeof Fn>[0]` 做一次形状收窄（编译期擦除，请求体不变）；写死具体类名会在绑定重新生成后失配。另外 Vue 3 中 `v-if` 先于 `v-for` 求值，需要按迭代变量过滤时请把 `v-if` 放进 `<template v-for>` 内层。
- CI（`.github/workflows/ci.yml`）会自动执行 Go 双构建 / 双测试 / vet / 前端类型与构建 / 密钥扫描（gitleaks）。**请勿提交任何明文密钥、密码或真实个人信息**——实验脚本与含凭据的文件一律放在 `.gitignore` 覆盖路径或本地。

## 七、提交规范
- 遵循绞杀者模式：每个阶段的改动独立提交、可回滚；**不要一次 Big Bang 重写**。
- 提交信息用中文或英文均可，但请说明「做了什么 / 为什么 / 如何验证」。
- 涉及 `kernel` / `ports` / `orderschema` 等核心契约的变更，请在 PR 描述里说明对现有扩展点的影响。
