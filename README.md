# OnePoint / order_server 后端

模块化的「从聊天群聊里识别订单」后端。原本是一个 Wails（Go + Vue3）桌面应用，
现正演进为**接口驱动、方便第三方扩展、可直接开源**的后端架构。

> 本仓库当前处于**架构重构期**（分支 `feature/universal-order-schema`）。
> 既有桌面应用功能保持不变，新增的 `go/kernel` / `go/ports` / `go/orderschema` / `go/compose` / `go/sources` / `go/config`
> 是面向「任何订单类型、任何数据源、任何 AI」的通用后端骨架。详细阶段计划见 [`REFACTOR_PLAN.md`](./REFACTOR_PLAN.md)。

## 设计目标
- **识别任意订单类型**：不再写死「生鲜团购」字段，模板由样例归纳或人工定义，路由自动匹配。
- **第三方可扩展**：加一个数据源 / AI 提供商 / 模板，只需新增一个包 + 一处注入，**不改任何现有文件**。
- **开源合规**：微信相关的取密钥 / 解密代码默认不链入开源构建（见 [`NOTICE`](./NOTICE) 与下方「构建标签」）。

## 分层架构（依赖方向严格单向）

```
┌──────────────────────────────────────────────────────────┐
│  delivery（Wails 绑定 / CLI / Web）—— 只编排端口，不含业务  │
├──────────────────────────────────────────────────────────┤
│  modules（业务模块，依赖 ports）                            │
│   wechatorder · checkout · dashboard · 订单操作 · 账单 …    │
├──────────────────────────────────────────────────────────┤
│  ports（端口契约：ChatSource / CompletionProvider /         │
│         PipelineStage / ConfigProvider）—— 新增模块只实现   │
│         接口 + 注册                                        │
├──────────────────────────────────────────────────────────┤
│  orderschema（领域内核：模板 / 订单 / 路由 / 归纳 / 校验）   │
├──────────────────────────────────────────────────────────┤
│  kernel（零业务依赖基础设施：Logger / Lifecycle /          │
│         WorkerPool / EventSink）                           │
└──────────────────────────────────────────────────────────┘
```

## 核心包导览
| 包 | 职责 |
|----|------|
| `go/kernel` | 零业务依赖基建：日志接口、可取消生命周期、有界 Worker 池、进程内事件总线。 |
| `go/orderschema` | 领域内核：订单模板（FieldDef/Template）、订单记录、签名自动路由（Route/RouteBest）、启发式+LLM 字段归纳（InferTemplate/LLMProposer）、校验/去重/镜像同步、Store 接口（MemoryStore/GormStore + 版本化迁移）。 |
| `go/ports` | 部署相关的扩展端口：ChatSource / CompletionProvider / PipelineStage / ConfigProvider。 |
| `go/compose` | 编排层：把「数据源 + AI + 存储」串成「导出 → 自动路由 → 抽取 → 入库」的后端（Engine）。 |
| `go/sources/csv` | ChatSource 参考实现：读 CSV / JSONL，证明「新增来源只加一个包不碰现有文件」。 |
| `go/sources/wechat` | ChatSource 的微信**唯一**实现；默认构建也参与编译与测试，隔离边界在 `go/wechatapi` 门面，不链入 `go/wechatlog`。 |
| `go/config` | ConfigProvider 的内存实现（命名空间 + 热更新广播）。 |

## 快速开始（默认构建，不含微信）
```bash
# 构建后端（默认不链入微信取密钥代码）
go build ./...

# 跑测试
go test ./...

# 跑一个「从 CSV 识别订单」的最小示例（见 examples/schema-studio）
go run ./examples/schema-studio
```

## 构建标签（build tags）
- **默认构建** `go build ./...` / `go build ./go/...`：**不链入** `go/wechatlog`（微信内存取密钥 / 数据库解密代码）。微信相关入口返回明确降级提示，其余功能照常。
- **带微信能力** `go build -tags wechat ./...`：启用 `go/wechatapi` 的真实实现；`go/sources/wechat` 在两种构建下都可用，只是默认构建走门面的降级错误。
- **桌面应用无需手动加标签**：`wails.json` 的 `build:tags` 已固定为 `wechat`，`wails build` / `wails dev` 自动带上，发版流程不变。

> 隔离边界在 [`go/wechatapi`](./go/wechatapi)：全仓只有 `enabled.go`（`//go:build wechat`）直接 import `go/wechatlog`。
> 可用 `go list -deps .` 验证默认依赖图不含该包；`-tags wechat` 下则应出现。
> 无论是否构建，`go/wechatlog` 的源代码随仓库发布，已按 Apache-2.0 在 [`NOTICE`](./NOTICE) 中署名。

## 模板工作台：设计期与运行期分离
`compose` 提供三种引擎，让「AI 配置」与「聊天源配置」互不绑架：

| 构造函数 | 依赖 | 用途 |
|---|---|---|
| `NewRecognitionEngine` | ChatSource + Store | **运行期**：按已发布模板路由 + 确定性规则抽取，**不调用 AI** |
| `NewInferenceEngine` | AI + Store | **设计期**：从样例归纳模板草案，**不需要聊天源** |
| `NewEngine` | 三者都要 | 兼容入口，原契约与缺依赖校验保持不变 |

因此**没配任何 API Key 也能正常识别订单**，没接微信也能先用样例推断模板。模板工作台还支持：

- **试跑**（`RunDry` / `RunOptions.DryRun`）：完整抽取但**不写 `orders_v2`**，不占用去重水位，结果只读展示。
- **数据源可插拔**：内置 `wechat` 与 `file`（CSV/TSV/JSONL）；第三方在自己的 `init()` 里 `schemaservice.MustRegisterSource(name, factory)` 一行注册，前端下拉自动出现。
- **可取消**：识别任务挂在 `kernel.Lifecycle` 的 ctx 上，关窗口自动中止，也可在界面点「取消」；取消不写任何数据。

模板工作台是**与旧链路并行的独立新应用**（只读写 `order_templates` / `orders_v2`），一键识别 / 账单 / 看板一行未改；何时切换属产品决策。

## 第三方如何扩展（不碰现有文件）
- 新增数据源：实现 `ports.ChatSource`，并按上表注册（`schemaservice.RegisterSource`）或直接注入 `compose.Engine`，参考 [`go/sources/csv`](./go/sources/csv)。
- 新增 AI：实现 `ports.CompletionProvider`（OpenAI 兼容）并 `ai.Register(...)`，参考 [`go/ai`](./go/ai)；也可用 `ai.NewProvider(ai.ProviderConfig{...})` 配置化接入。
- 新增模板：用 `orderschema.InferTemplate` 从样例归纳，或手工发布 `orderschema.Template`。
- 详见 [`CONTRIBUTING.md`](./CONTRIBUTING.md)。

## AI 调用层（`go/ai`）
`ai.Provider` 把重试（指数退避 + 抖动，4xx 与取消不重试，5xx/429/网络错误才重试）、超时、限流（`MaxConcurrency`）、流式、token 与成本统计收在一处；调用方的 `ctx` 一路贯穿到 HTTP 请求，取消可立即中断在途调用，不用等满超时。

- **默认模型**：`CompletionRequest.Model` 为空时使用 `ProviderConfig.Model`，因此 `compose.CompletionFunc` 无需知道模型名即可工作。
- **主备高可用**：`ai.NewFallback(primary, backup)` 把多个 provider 串成降级链，对 `compose.Engine` 完全透明；主链路连续失败后自动切备用。为保证不出现重复文本，**流式请求不降级**，ctx 已取消时也不再打备用端点。
- **底层传输**：`go/utils/httputils` 的 `AiRequestCtx` / `AiStreamRequestCtx` 供新链路使用（带状态码校验与取消语义）；旧 `AiRequest` / `AiStreamRequest` 保持原有行为，供旧识别链路继续使用。
- 模板工作台已在自动模式下接入主备降级（两个 Key 都配置时）；显式指定 provider 时不做隐式切换。

## 开源与合规
- 项目主许可：**MIT**（[`LICENSE`](./LICENSE)）。
- 第三方署名与微信能力合规声明：[`NOTICE`](./NOTICE)；其中 `go/wechatlog` 改编自 sjzar/chatlog（Apache-2.0，全文见 [`LICENSE-THIRD-PARTY`](./LICENSE-THIRD-PARTY)）。
- CI 已配置构建 / 测试 / vet / 密钥扫描（gitleaks）。

## 目录之外
- 前端（Vue3）位于 `frontend/`，通过 Wails 绑定调用 Go 后端。
- 微信本地数据库解密与密钥获取相关逻辑在 `go/wechatlog`（合规敏感，见上）。
- 桌面应用完整构建请用 `wails build`（需 Node 环境与 `frontend/` 依赖）。
