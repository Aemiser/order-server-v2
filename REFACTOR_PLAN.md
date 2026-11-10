# 后端模块化重构计划（阶段性）

> 目标：把现有「Wails 桌面应用 + 业务散落全局」的后端，重构成**模块化、接口驱动、方便第三方扩展、可直接开源**的后端架构。
> 原则：绞杀者模式（Strangler）——新增 `kernel`/`ports` 层与适配器，**不改调用点就零行为变化**；每阶段可编译、可回归、可回滚，绝不一次 Big Bang。
> 分支：`feature/universal-order-schema`（基于 `ptimize@930fbad` 同步而来）。
> 当前进度（2026-11-10）：Phase 0 / 1 / 2 / 2.5 / 2.6 / 3 / 5 / 6 / 7 / 8.5 / 10 / 11 / 12 已交付；剩余 Phase 4 / 8 / 9 均为「旧链路绞杀者接线」。新链路已能独立、无 AI 依赖地跑通（含文件数据源与试跑），旧的一键识别 / 账单 / 看板一行未动。Phase 11 是工程门禁收口（前端类型债务清零 + CI 门禁），Phase 12 是数据源层收口（唯一 ChatSource 实现），两者都不属于架构切换。
>
> **产品决策（2026-11-10）**：本分支是「全新应用」，**不做新旧链路切换 / 灰度 / 影子对照**。切换时机属产品意图，不由架构重构代劳；因此 Phase 6 收口为「新链路自身独立可用」，而不是把旧入口改成分流。等用户明确要求切换时再单独立项。

---

## 一、目标架构（依赖方向严格单向）

```
┌──────────────────────────────────────────────────────────┐
│  delivery（Wails 绑定 / CLI / Web）—— 只编排端口，不含业务  │
├──────────────────────────────────────────────────────────┤
│  modules（业务模块，依赖 ports）                            │
│   wechatorder · checkout · dashboard · 订单操作 · 账单 …    │
├──────────────────────────────────────────────────────────┤
│  ports（端口契约：ChatSource / CompletionProvider /         │
│         TemplateStore / OrderRepository / PipelineStage /  │
│         ConfigProvider）—— 新增模块只实现接口 + 注册        │
├──────────────────────────────────────────────────────────┤
│  kernel（零业务依赖基础设施：Logger / Lifecycle /          │
│         WorkerPool / EventSink）—— 已被主程序接线           │
└──────────────────────────────────────────────────────────┘
       依赖方向：上 → 下；绝不允许具体实现硬编码散落各处
```

判定标准（开源就绪）：**第三方开发者加一个新模块，能否只新增一个包 + 一行注册，不改任何现有文件？**

---

## 二、已完成阶段

### Phase 0 · 同步主线（✅ 完成）
- 把主程序 `ptimize@930fbad`（v10.0.13：订单操作识别 / 自定义术语规则 / 微信 3.9 适配）合并进本分支；覆盖主程序最新 4 个未提交前端文件与真实配置。
- 地基包 `go/orderschema`（通用订单 schema：FieldDef/Template/OrderV2/字段别名收敛/样例归纳/路由）作为独立、零 global 的参考实现已就绪。

### Phase 1 · 止血 P0（✅ 完成，全部测试通过）
| # | 问题 | 修复 | 验证 |
|---|------|------|------|
| 1 | 正常启动不建业务表/看板索引，导致看板增量优化静默失效 | `flags.EnsureBusinessSchema()` 在正常启动也调用（`main.go` + `flags_db.go`） | 全量构建 + dashboard 测试通过 |
| 2 | `CreateOrder` 先标记 `Isstorage` 再写库，写库失败静默丢单 | 改为**先写库、成功后才标记**（`loaddb.go`） | 现有测试全绿 |
| 3a | `app.go` 两处 `recover()` 静默吞 panic | 改用 `kernel.RecoverLogrus` 记录堆栈 | 全量构建通过 |
| 3b | 关窗口后 worker 无限跑、100 个 AI 请求停不下 | `kernel.Lifecycle` + `OnBeforeClose` 优雅关闭；`httputils` 基 context 可取消；`dashboard.StopWorker()` | 全量构建通过 |
| 4 | 凭据只靠 `.gitignore`，无 CI | 新增 `.github/workflows/ci.yml`（构建/测试/vet/密钥扫描）+ `.golangci.yml` | YAML 合规 |

### Phase 2 · 基础设施模块 + 端口契约（✅ 完成骨架）
- 新增 `go/kernel/`：`log.go`（Logger 接口 + logrus 适配器 + Recover）、`lifecycle.go`（Lifecycle 可取消 ctx/WaitGroup/关闭钩子）、`async.go`（WorkerPool 有界并发 + 背压 + ctx 取消）、`events.go`（EventSink 进程内总线）。**零业务依赖**。
- 新增 `go/ports/`：定义 4 个真正"部署相关"的扩展端口（ChatSource / CompletionProvider / PipelineStage / ConfigProvider）。订单模板与订单持久化的契约**不重复定义**——直接复用 `go/orderschema.Store`（模板+订单二合一，由 MemoryStore/GormStore 共同实现）。
- 已接线的部分：`main.go` 用 Lifecycle 做优雅退出；`app.go` 用 RecoverLogrus；`httputils` 用可取消基 context；`dashboard.worker` 支持停止。
- 配套单测：`go/kernel/kernel_test.go`（日志/生命周期/worker 池/事件总线/恢复）全绿。

### Phase 2.5 · 编排层 compose（✅ 完成纵向切片，全链路测试通过）
- 新增 `go/compose/`：把 `ChatSource` + `CompletionProvider` + `orderschema.Store` 编排成可用的"导出 → 自动路由 → 抽取 → 入库"后端。
  - `engine.go`：`Engine`（`NewEngine` 校验三依赖不可缺）+ `Run`（按 `exportStage → routeStage → persistStage` 串联，每阶段皆 `ports.PipelineStage`）+ `InferTemplate`（AI 注入归纳）。
  - `adapter.go`：`CompletionFunc` 把任意 `ports.CompletionProvider` 适配成 `orderschema` 的 `CompletionFunc` 注入点——印证 orderschema 文档"上层把 DeepSeek/Qwen 包一层注入即可"，且不依赖具体 AI 实现。
  - `compose_test.go`：内存 ChatSource + 桩 Provider 跑通端到端（生鲜模板只命中生鲜消息、预约消息进人工审查、镜像列自动同步、同消息去重），并验证 AI 注入路径与缺依赖拒绝构造。
- 关键发现：**`go/orderschema` 已是完备的领域内核**，自带 Store 接口（模板+订单持久化，含 MemoryStore/GormStore 与迁移记录 `SchemaMigration`）、签名自动路由（`Route`/`RouteBest`）、启发式+LLM 字段归纳（`InferTemplate`/`LLMProposer`）、校验/去重/镜像同步。**因此 Phase 4/6 的存储与模板契约无需重建，主程序只需通过 strangler 逐步改用 `orderschema.Store` 与 `compose.Engine`。**

### Phase 2.6 · 可插拔模块落地（✅ 完成：ConfigProvider 实现 + 微信可选插件 + LICENSE）
- 新增 `go/config/provider.go`：`ports.ConfigProvider` 的内存实现 `Provider`（命名空间 → 键 → 值，支持 `Get`/`Set`/`Watch` 热更新广播，单监听 panic 不影响其余），替掉未来写死的 `cfgSections`。配套 `config_test.go`（`GetSet`/`Watch`）全绿。
- 新增 `go/sources/wechat/wechat.go`：`//go:build wechat` 隔离的可选插件，把 `go/wechatlog.Export` 适配为 `ports.ChatSource`（`go build -tags wechat` 可编译，默认排除）。**此处记录的是当时状态**：该包的门面依赖由 Phase 7 收口、构建标签与接线由 Phase 12 调整。
  - 注：本阶段只隔离了「新增适配器」；主程序原有的直接依赖直到 Phase 7 才收口，「默认不链入」届时才是编译期事实。
- 新增根 `LICENSE`：MIT（Copyright (c) 2026 Aemiser），开源合规基线。
- 提交：`030608a`。验证：`go test ./go/...` 全绿；默认 `go build ./...` 不引入微信包。

### Phase 3 · 配置模块（ConfigProvider）（✅ 完成，2026-11-10）
- **配置节自注册**：新增 `go/config/sections.go`——`Section` 描述符（表名 / SQL 别名 / MD5 列清单）、`Register`/`MustRegister`/`Sections`/`Names`/`SectionByAlias`，以及按注册表动态拼装的 `RevisionSQL()`；内置 5 节在 `go/core/cfg_sections.go` 的 `init()` 里注册，并同时注册各表行加载器。第三方加一节只需在自己的 `init()` 里 `config.MustRegister`，不必改 core。
- **判定链路去硬编码**：`cfg_revision.go` 删掉写死的 `cfgSections` 与 `cfgSectionMD5SQL`，改为动态 SQL + `Rows()` 通用扫描到 map；`cfg_watcher.go` 的 revision / 变更摘要、`init_cfg.go` 的增量拉取全部按注册表驱动。注册项做重名 / 别名冲突 / SQL 标识符校验，避免「加了一节却静默漏检」。
- **Provider 接线**：新增 `go/core/cfg_provider.go`，`applyCfgRows` 发布快照后把非敏感投影写进 `config.Provider` 并触发 `Watch`；对外暴露 `core.CfgProvider() ports.ConfigProvider`。`global.CfgSnapshot` 仍是唯一真值来源，Provider 只是只读视图，**不投影 API key / 提示词正文 / 客户数据**。
- **零行为变化**：`TestRevisionSQLMatchesLegacy` 把动态 SQL 与改造前原 SQL 归一化后逐字比对；逐表 MD5 快比对、增量拉取、内容指纹复核、降级全量比对机制一行未改。
- 验证：`go test ./go/... -count=1`（15 包全绿）、`go vet ./go/...`、`go build ./...` 通过；新增注册表 / 投影 / SQL 等价 / 节清单等用例。

> **集成侧已收口（Phase 7，2026-11-10）**：主程序对 `go/wechatlog` 的直接依赖已全部收进 `go/wechatapi` 的 `//go:build wechat` 实现，默认构建依赖图不再包含该包；桌面构建由 `wails.json` 的 `build:tags` 固定带 `wechat`，发版行为不变。

---

## 三、其余阶段（含已交付补记与待执行项，状态见各条；每阶段：实现 → `go build ./...` → 现有测试回归 → 必要时补单测 → 可回滚）

### Phase 4 · 数据库模块（主程序接线 orderschema.Store）
- `orderschema.Store` 已提供模板+订单持久化契约（MemoryStore/GormStore + `SchemaMigration` 版本化迁移），**无需新建 OrderRepository 端口**。
- 剩余工作（strangler，非重建）：把 `order`/`checkout`/`dashboard` 对 `*gorm.DB` 的散用逐步替换为 `orderschema.GormStore`（在 `go/compose` 或新 `go/storage` 包注入 `*gorm.DB` 包一层即可，GormStore 已具备）；保留既有 `AddressInfo` 等表不动，新订单走 `orders_v2`。
- 验证：订单入库/账单/看板查询回归；仓储单测已可用 MemoryStore。

### Phase 5 · AI 模块（CompletionProvider）（✅ 完成，2026-11-10）
- **背景**：`go/ai` 并非从零建——通用 OpenAI 兼容 `Provider` 与注册表在 Phase 2 已落地，`schemaservice` 也已在用它。但代码核查发现注释里承诺的能力有四处没兑现，其中一处是功能性缺陷。本阶段按「补齐实现 + 零行为变化的传输层」收口，未新建平行的 `DeepSeekProvider`/`QwenProvider`（两者只是同一 `ProviderConfig` 的不同配置，独立建类属重复）。
- **修复的真实缺陷**：
  1. `buildPayload` 是自由函数，只看 `req.Model`；而 `compose.CompletionFunc` 刻意不指定模型 → 请求体出现 `"model": "default"`，真实 API 拒绝，**模板归纳的 LLM 路径实际静默退化为启发式**。改为 `Provider` 方法，空模型时用 `cfg.Model`，仅两者皆空才兜底 `"default"`。
  2. `Complete(ctx, ...)` 的 ctx 是摆设：底层 `httputils` 用包级 `baseCtx`，只在请求**返回后**才检查取消，关窗口时在途请求要等满超时（Phase 1「100 个 AI 请求停不下」的残留）。新增 `AiRequestCtx` / `AiStreamRequestCtx` 让 ctx 一路贯穿到 `http.NewRequestWithContext`。
  3. 端口注释承诺的**限流 / 降级**未实现：补 `MaxConcurrency` 信号量（可被 ctx 取消）与 `ai.NewFallback` 主备组合器。
  4. 重试无退避且对 4xx 也重试；流式不检查 HTTP 状态码，非 200 把错误 JSON 当 SSE 吞掉，表现为「空结果」。
- **传输层零回归**：`httputils` 抽出内部 `aiRequest` / `aiStreamRequest`，旧 `AiRequest` / `AiStreamRequest` 仍走 `baseCtx`、流式仍不查状态码（`strict=false`），行为逐字不变，`TestLegacyAiRequestErrorTextUnchanged` / `TestLegacyStreamKeepsBehaviourOnNon200` 锁住。新入口独享类型化 `HTTPStatusError`（`Error()` 文本与旧版一致）、状态码校验与取消返回。**旧识别链路（`ai_model/deepseek.go`、`qwen.go`、`wechatorder/ai.go` 等）一行未改**。
- **能力内聚**：指数退避 + 半抖动（可封顶）、错误分类（4xx / ctx 取消不重试，5xx / 429 / 网络错误重试）、自定义 Headers、成本与 token 统计。流式**只在「一个字都还没吐出」时重试**，中途失败带错返回，避免重试/降级造成重复文本；`FallbackProvider` 对流式请求不降级、ctx 取消不降级。注册表补 nil / 空名校验与 `MustRegister`、`List` 稳定排序。
- **接线**：`schemaservice.newAIProvider` 传真实模型名、限流 8、退避参数；自动模式（前端未指定）且两个 Key 都配置时用 `NewFallback` 组成主备链，主模型连续失败自动切备用；显式指定 provider 时尊重用户精确选择，不隐式切换。
- 验证：mock HTTP 单测覆盖模型默认值/覆盖、ctx 取消在途请求、重试分类与退避封顶、并发限流、流式非 200 / 空首包重试 / 吐字后不重试、降级全路径、注册表校验；`-race` 通过；双构建全量回归。

### Phase 6 · 模板模块（主程序接线 orderschema）（✅ 完成，2026-11-10）
- **范围调整**：原计划写「把一键识别前端流程逐步接到 orderschema + compose」，但用户已定「全新应用、不改造老链路」，且 Phase 8.5 的模板工作台已经把新链路接进主程序。本阶段因此**不做旧入口分流**，改为让新链路自身达到可独立验证、可安全试用的状态；切换决策见顶部产品决策。
- **修复的契约级缺陷**：核查发现 `schemaservice.buildEngine` 在**运行期**也强制 `aiFactory` 成功，可前端明明写着「AI 提供方（仅推断用）」——没配任何 API Key 时，纯规则识别直接跑不起来，与用户拍板的「格式归纳是设计期一次性动作，运行期只做路由 + 按已注册 schema 抽取」冲突；「从样例推断」即使用纯启发式，也先建了一个需要 AI 的引擎，等于被 AI 配置绑架。
- **设计期 / 运行期解耦**：`compose` 新增三种引擎——`NewEngine`（两者兼备，保留原契约与缺依赖校验）、`NewRecognitionEngine`（ChatSource + Store，无 AI）、`NewInferenceEngine`（AI + Store，无聊天源）；误用会明确报错而非空指针。`schemaservice` 的运行走前者、归纳走后者，互不依赖对方的配置。
- **可独立验证**：`Engine.RunDry` / `RunOptions.DryRun` 完整抽取但**不写库**，不污染 `orders_v2`、不占用去重水位；前端试跑结果只读展示（订单行隐藏操作）。
- **数据源可插拔**：新增 `go/schemaservice/sources.go` 注册表（`RegisterSource`/`MustRegisterSource`/`Sources`），内置 `wechat` 与 `file`（接早已存在却未接线的 `go/sources/csv`，支持 CSV/TSV/JSONL）；第三方一行注册即可接入，未知来源明确报错而不静默回退。前端下拉由 `SchemaListSources` 动态拉取。
- **生命周期闭环**：`StartRun` 不再用 `context.Background()`，`main.go` 提前创建 `kernel.Lifecycle` 并经 `InitWithContext` 注入；阶段间、逐条路由、逐笔入库均检查 ctx，关窗口自动取消，另提供 `SchemaCancelRun` 手动取消。取消标记 `Cancelled` 而非 `Error`，且不写任何数据。
- **UI 同步**：移除运行表单里误导性的「AI 提供方」下拉（归纳对话框自带），新增来源选择 / 文件路径 / 试跑开关 / 取消按钮；`OrderRows` 增加 `readonly`，试跑订单不可误操作。
- 验证：新增 compose 4 例 + schemaservice 5 组用例，覆盖无 AI 运行、试跑零入库、取消不写库、纯启发式/AI 归纳不依赖聊天源、文件源端到端、注册表校验与路径校验；双构建各 17 包测试全绿、`go vet` 仅既有告警、`vite build` 与 `wails build`（Tags `[wechat]`）通过。`vue-tsc` 有 12 条**既有**类型错误（均在未改动的 `CheckTable` / `OrderTable` / `InferDialog` / `TemplateEditorDialog`），本次改动零新增，未纳入本阶段处理；已在 Phase 11 统一清零并加 CI 门禁。
- **旧链路一行未改**：一键识别 / 账单审核 / 看板 / 粗筛规则全部保持原样。

### Phase 7 · 导出模块（ChatSource，微信做成可选插件）（✅ 完成，2026-11-10）
- **门面 + 双实现**：新增 `go/wechatapi`，对外类型（`Options`/`Message`/`Result`/`KeyResolution`）全部自带，**不 import `wechatlog`**。
  - `enabled.go`（`//go:build wechat`）：逐字段转发到 `go/wechatlog`，行为与改造前完全一致；`mapping_test.go` 用 `reflect.DeepEqual` 兜底防止将来漏映射字段。
  - `disabled.go`（默认构建）：导出/密钥操作统一返回 `ErrDisabled`，状态类 API 返回带 `message` 的降级结构，投放监听是空操作——**绝不静默返回空结果**，避免一键识别把「没导出」误判成「没有订单」。
- **4 个直接依赖点全部切换**：`app.go`（9 处，另在启动检查 / 投放目录加 `Enabled` 判断避免空转与 `MkdirAll("")`）、`go/server/wechatorder/export.go`、`go/schemaservice/wechat.go`、`go/sources/wechat/wechat.go`。全仓只剩 `go/wechatapi/enabled.go`（及带 tag 的映射测试）import `go/wechatlog`。
- **发版链路零改动**：`wails.json` 增加 `"build:tags": "wechat"`（Wails v2 的 `build.go` / `dev.go` 都会把它与命令行 `-tags` 合并）。`wails build -dryrun` 实测 `Tags | [wechat]`，故 `wails build` / `wails dev` 自动带微信能力，发布脚本无需改命令，只在 `.NOTES` 补了 ASCII 防呆说明。
- **CI 双路径**：`.github/workflows/ci.yml` 新增「默认依赖图不含 wechatlog」断言 + `-tags wechat` 的 build / test；`README.md` 重写构建标签章节。
- 验证：`go build ./...` 与 `go build -tags wechat ./...` 均通过；两种构建各 16 个测试包全绿；`GOOS=linux` 交叉编译双路径通过（CI 不会挂）；`go list -deps .` 默认不含、`-tags wechat` 含 31 个相关包；`wails build -dryrun` 标签正确。
- 说明：新链路 `schemaservice` 当时保留自带的 `wechatSource`（与 `go/sources/wechat` 等价、共用同一门面），本阶段**未合并重复适配器**，以免把行为差异（Group 空校验 / Days 计算 / 错误包装）带进旧链路回归；该项已在 **Phase 12** 用 characterization 测试锁住行为后收口。

### Phase 8 · 中间件流水线（PipelineStage）— 骨架已有，旧链路未接
- 现状：`compose/engine.go` 已用 3 个 `PipelineStage`（Export / Route / Persist）串起新链路；旧的一键识别里粗筛、订单操作规则、路由分发仍是散落的 switch。
- 余下：粗筛 / 订单操作规则 / 路由分发统一抽象为 `PipelineStage` 链；操作规则做可配置策略；分发复用 Phase 6 的 Resolver。
- 验证：一键识别链路回归；补 Stage 单测。

### Phase 8.5 · 模板工作台纵切片（✅ 完成，2026-11-10）
- **模板工作台纵切片交付**：`go/schemaservice` 把 orderschema.GormStore + compose.Engine 接进主程序（模板 CRUD/发布/停用、样例推断、识别运行、orders_v2 查询），app.go 新增 11 个 `Schema*` 绑定；前端新增「模板工作台」视图（SchemaStudioView + 编辑器 + 推断对话框 + 通用订单表）。真库验证：三张新表幂等创建、识别端到端测试绿、`wails build` 产出可运行 OnePoint.exe。旧链路零改动，新旧并行可对比。

### Phase 9 · 交付层收口（Wails 绑定）
- `*App` 的 55 个绑定方法改为只编排端口，业务代码不再 `import wails runtime`、不再用包级全局存 UI context。
- 验证：前端全流程回归（需 `npm install` + `wails build`）。

### Phase 10 · 开源就绪（✅ 基本完成，剩去品牌化决策）
- ✅ 已完成：
  - `LICENSE`（MIT）、`NOTICE`（sjzar/chatlog Apache-2.0 署名 + 微信能力构建/分发状态如实声明）、`LICENSE-THIRD-PARTY`（Apache-2.0 全文）、`CONTRIBUTING.md`（扩展指南 + build tag 约定）、`README.md`（架构 + 构建 + 扩展导览，替换 Wails 模板）。
  - `examples/schema-studio`：最小可运行扩展示例（自定义 ChatSource + CompletionProvider + 内存 Store → compose.Engine 全链路），证明「不碰现有文件即可扩展」。
  - 清理：删除 `go/testdata/`（含真实 RDS 密码）、`docs/微信密钥获取步骤.md`（真实手机号 PII + 专有流程）、根目录 710 行 CDN 原型 `index.html`、`cmd/keyscan`/`keyfind2`/`matchdb`/`verifykey`（一次性密钥诊断探针）；保留 `cmd/cfginit`（部署工具）与 `cmd/schemademo`（架构演示）。
  - 测试样例 PII 脱敏（compose_test / schemademo / example 中真实地名与真实客户手机号 → 虚构值）。
- 🟡 余下（待用户拍板）：业务名去品牌化。实测 8 个文件仍有「帅涛吃什么 / 1點點」硬编码（`wechatorder/config.go`、`rule.go`、`ai_test.go`、`rule_test.go`、`AppSettingsView.vue`、`cfg_seed.json`、`wechatlog/wcdb_parse.go`、`weflow_test.go`）及 `frontend/` 内业务文案；是否替换、替换成什么属业务决策，不由架构重构代劳。
- 验证：`go build ./...` + `go test ./...`（14 包）全绿；gitleaks 由 CI 执行（本机未装，已人工扫描 tracked 文件无明文凭据）。

### Phase 11 · 前端类型门禁收口（✅ 完成，2026-11-10）
- **范围**：不涉及架构阶段切换，只清掉长期挂账的前端类型债务并防止回归；切换类阶段（4 / 8 / 9）仍按顶部产品决策冻结。
- **既有类型债务清零**（`npm run lint` 从 13 条诊断降到 0）：
  1. `CheckTable.vue`：`Product.specs` 声明为 `string[]`，但后端 `checkout.Spec` 实为对象（带 `Name`），`transformData` 也确实产出 `{name}` 数组——类型与运行时契约不符，模板里 `spec.name || spec` 因而不成立。改为 `SpecValue = string | SpecObject`，新增 `specLabel()` 统一取显示名，表头 / 价格提交共用；数据转换结果不变，顺带消掉 `spec` 可能为 null 的告警。
  2. `OrderTable.vue`：两处分页把 `v-if` 与 `v-for` 写在同一元素上，Vue 3 中 `v-if` 先求值、`page` 不在作用域内，中间页码实际一个都不渲染（**真运行时缺陷**）。改为 `<template v-for>` 包裹、把 `v-if` 放进内层，分页算法本身不动。
  3. Wails 生成类型的入参形状：`ordertutil.ExceladdressInfo` 带 `createdAt` / `convertValues`，`schemaservice.SaveTemplateInput` 带 `convertValues`；Go 侧 JSON 反序列化只需要数据字段，业务组件传纯对象才是正确用法。在 3 处调用点用 `as unknown as Parameters<typeof Fn>[0]` 收窄——编译期擦除、请求体不变，既不手改生成文件，也不引入运行时代理；用 `Parameters<...>` 而非具体类名，绑定重新生成后依然成立。
- **CI 门禁**：新增 `frontend` job（Node 22 + `npm ci` + `npm run lint` + `npm run build`），`vue-tsc` 从此是合入前必过项；`vite build` 是纯静态编译，不需要 Wails 运行时。`CONTRIBUTING.md` 第六章同步说明该约定与 Wails 生成类型的处理方式。
- **纪律**：未改任何后端业务链路，未改一键识别 / 账单 / 看板的触发时机与数据流；`frontend/wailsjs/` 生成文件一行未改。
- 验证：`npm run lint` / `npm run build` 全绿；`go build ./...` 与 `-tags wechat` 双构建通过、双构建各 17 包测试全绿；`go vet` 仅既有 `wcdb_windows.go:51 unsafe.Pointer`；`wails build`（Tags `[wechat]`）通过，重新生成绑定后类型检查仍全绿。

### Phase 12 · 数据源层收口（✅ 完成，2026-11-10）
- **起点**：`go/sources/wechat` 自 Phase 2.6 建好后**从未被任何代码引用**，且带 `//go:build wechat`，默认构建根本不编译；真正在跑的是 `schemaservice/wechat.go` 里另写的一份 `wechatSource`。两份等价实现在 Group 空校验 / Days 推导 / 错误包装上悄悄分叉，Phase 7 已明确留作后续清理。
- **统一实现**：`go/sources/wechat` 去掉 build tag，成为微信 ChatSource 的**唯一**实现。隔离边界只留在 `go/wechatapi` 门面——本包仅 import `ports` + 门面，默认构建也参与编译与测试；未启用微信能力时门面的 `ErrDisabled` 被包装成用户可见文案，绝不静默当成「没有消息」。
- **行为不再隐式分叉**：把 `schemaservice` 版三处口径搬进统一实现，由 `wechat_test.go` 的 characterization 用例锁住——日期窗口推导 Days（非法格式 / 倒置区间保留默认值）、群名为空先报配置提示且不发起导出、导出错误统一 `导出聊天记录失败: %w` 并保留 `errors.Is`。原先只在孤儿包里的 `opt.DataDir` 覆盖改为显式 `WithDataDirOverride`：模板工作台的 `DataDir` 语义是「文件数据源路径」，选 wechat 源却填路径时不能把文件路径错当微信数据目录，需要动态指定的宿主可显式开启。
- **可测性**：新增 `ExportFunc` 注入点，测试不依赖微信数据库、也不依赖构建标签，双构建都能跑；`Export` 前置检查 `ctx.Done()`，取消后不再发起导出。`schemaservice/wechat.go` 只保留「配置 → 门面选项」的转换，`wechat_source_test.go` 锁定注册表里的 wechat 指向统一实现，防止再加 tag 或写第二份适配器。
- **CI**：默认依赖图断言加入 `./go/sources/wechat`，锁死去 tag 后仍不链入 `go/wechatlog`（实测默认 0 个、`-tags wechat` 31 个）。README / NOTICE / CONTRIBUTING 的构建标签表述同步。
- 验证：双构建各 **18 包**测试全绿（新增 `go/sources/wechat` 8 组用例 + `schemaservice` 1 例）、`go vet` 仅既有告警；`vite build` / `wails build`（Tags `[wechat]`）通过。旧识别 / 账单 / 看板零改动，未执行任何合并。

---

## 四、明确不改动（避免过度工程）
- `checkout.GetDaydata` 的 `Day()+1` 边界（疑似时区补偿，改了会动既有账单口径）。
- 微信 `.v3m` 缓存标记、看板增量加载逻辑（只修"索引没建出来"，不重写）。
- 进程内内存会话 + bcrypt(12)（桌面单实例刻意设计，不引入 JWT/OAuth）。
- 100 协程并发模型（无缓冲 channel 已有界，只补 ctx 取消，不引入令牌桶）。
- `cfg_*` 逐表 MD5 热更新机制（保留比对机制，只改"键由模块自注册"）。
- 旧链路的触发时机：一键识别 / 账单 / 看板何时切到新引擎（含功能开关、灰度、影子对照）。属产品决策，见顶部说明；本分支不把旧入口改成分流，也不在旧链路里插旁路钩子。

## 五、回滚纪律
- 每阶段在 `feature/universal-order-schema` 独立提交；发现问题 `git revert` 单阶段即可，不影响其余。
- 不触碰 `dev` 主分支（原 `ptimize` 已改名；按约定未获授权不合并）。
