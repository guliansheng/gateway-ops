# 渠道鉴权配置与 Sub2API Token 刷新实现计划

> **面向 AI 代理的工作者：** 使用 executing-plans 逐任务实现；每项遵循 TDD，修改后运行真实测试与构建。

**目标：** 让渠道登录 Header/参数可编辑，新增 NewAPI 访问令牌鉴权，并让 Sub2API access_token 在过期前通过 refresh_token 自动续期。

**架构：** Channel 保存共享登录请求模板；connector 只负责协议执行；channel.Service 负责凭据生命周期、兼容回退和刷新后的加密持久化。旧数据空模板与旧 token JSON 均按兼容规则读取。

**技术栈：** Go/Gin/GORM/Resty + React/TypeScript/Vite。

---

### 任务 1：登录模板模型与默认值

**文件：**
- 修改：`backend/internal/storage/model.go`
- 修改：`backend/internal/channel/service.go`
- 新建：`backend/internal/channel/auth_config_test.go`

- [ ] 添加 Channel 的 `LoginHeadersJSON`、`LoginParamsJSON` 字段。
- [ ] 定义 `RequestKV`、默认 NewAPI/Sub2API 模板、规范化与变量展开函数。
- [ ] 先写测试验证默认模板、空配置回退、变量替换和自定义值。
- [ ] 运行 `go test ./internal/channel -run AuthConfig -v`。

### 任务 2：API 与 connector 传递登录模板

**文件：**
- 修改：`backend/internal/api/channels.go`
- 修改：`backend/internal/connector/connector.go`
- 修改：`backend/internal/channel/service.go`
- 修改：`backend/internal/connector/newapi/newapi.go`
- 修改：`backend/internal/connector/sub2api/sub2api.go`
- 新建/修改 connector 测试文件。

- [ ] API create/update/view 支持 `login_headers`、`login_params`。
- [ ] `connector.Channel` 增加结构化 Headers/Params。
- [ ] NewAPI/Sub2API Login 使用模板构造 body/header；没有模板时等价于原行为。
- [ ] 保留 Turnstile 现有行为并验证不会回归。
- [ ] 运行相关 Go 测试。

### 任务 3：NewAPI 三种鉴权

**文件：**
- 修改：`backend/internal/channel/service.go`
- 修改：`backend/internal/connector/connector.go`
- 修改：`backend/internal/connector/newapi/newapi.go`
- 新建/修改测试。

- [ ] NewAPI token JSON 增加 `auth_type`，缺失时兼容旧 cookie 模式。
- [ ] 访问令牌模式保存 token 与自定义请求 Headers；默认 `Authorization: Bearer {{token}}`。
- [ ] `AuthSession` 增加通用 Header/鉴权类型字段；NewAPI `getJSON` 根据模式发请求。
- [ ] 测试旧 Cookie 凭据和新访问令牌均可构造正确请求。

### 任务 4：Sub2API refresh_token 生命周期

**文件：**
- 修改：`backend/internal/channel/service.go`
- 修改：`backend/internal/connector/connector.go`
- 修改：`backend/internal/connector/sub2api/sub2api.go`
- 修改：`backend/internal/storage/channels.go`（仅在需要原子凭据更新时）
- 新建/修改测试。

- [ ] Sub2API 凭据扩展为 access_token/refresh_token/expires_at。
- [ ] 登录响应读取 refresh_token 并保留 expires_in。
- [ ] connector 实现 refresh POST 调用并解析新 token。
- [ ] EnsureSession 在安全窗口内刷新；成功后加密写回完整凭据，失败不覆盖旧值。
- [ ] 为同一渠道/账号刷新加进程内互斥，避免并发旧响应覆盖新 token。
- [ ] 兼容旧 `{access_token}` JSON。

### 任务 5：渠道管理 UI

**文件：**
- 修改：`frontend/components/monitor/channel-form-dialog.tsx`
- 可能修改：前端渠道类型/API 类型所在文件。

- [ ] 添加动态 KV 编辑器（Headers/参数），支持增删改与恢复默认。
- [ ] 新建渠道自动填默认模板；编辑渠道加载后端配置。
- [ ] NewAPI token 模式增加 Cookie / 访问令牌子类型。
- [ ] 访问令牌默认 Header 为 Authorization/Bearer 模板。
- [ ] Sub2API 增加 Refresh Token 输入，并继续支持敏感字段“留空不修改”。
- [ ] 保持多账号表单逻辑可用。

### 任务 6：验证与文档

**文件：**
- 修改：`README.md`（渠道鉴权说明）

- [ ] `go test ./...`
- [ ] 前端执行项目既有 build/typecheck 命令。
- [ ] 检查 `git diff` 和 `git status`，确认无意外文件。
- [ ] 运行代码审查，修复高/中风险问题后再次测试。
