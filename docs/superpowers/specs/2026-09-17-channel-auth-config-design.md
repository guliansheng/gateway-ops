# 渠道鉴权配置与 Sub2API Token 刷新设计

## 目标

将 NewAPI / Sub2API 渠道登录中写死的 Header 与请求参数改为可编辑配置，同时保持默认行为和既有渠道兼容。NewAPI 新增长期有效的访问令牌方式；Sub2API token 模式增加 refresh_token，并在 access_token 过期前自动刷新并持久化最新 token。

## 方案选择

采用“结构化配置 + 默认模板 + 运行时变量替换”的方案，而不是自由文本 JSON 或完全脚本化请求。

1. 结构化键值列表：前端可增删改 Header 和参数，后端保存 JSON；可校验、可恢复默认，也便于隐藏敏感值。
2. 自由 JSON 文本：实现简单但 UI 可用性、校验、单项增删较差，不采用。
3. 脚本/表达式式请求：灵活度最高但超出当前需求且安全与维护成本高，不采用。

## 数据模型

Channel 新增登录请求配置字段，使用 JSON 文本存储：

- `login_headers_json`：Header 键值列表。
- `login_params_json`：登录参数键值列表。

ChannelAccount 继续复用父 Channel 的登录请求模板，不单独保存 Header/参数；账号自身仅提供用户名、密码、token 等变量值。这样同一渠道下多账号不会复制相同模板。

运行时支持变量：

- `{{username}}`
- `{{password}}`
- `{{turnstile_token}}`

保存的是模板而不是展开后的真实密码。新建渠道和“恢复默认”都由前后端共同使用按渠道类型定义的默认模板；旧数据库记录两个字段为空时，后端自动回退到相同默认模板，因此升级不破坏已有渠道。

默认值：

NewAPI：

- Header：`Content-Type = application/json`
- 参数：`username = {{username}}`、`password = {{password}}`
- Turnstile 保持现有语义：存在 token 时仍作为 query 参数 `turnstile=<token>`，本次不将 Turnstile 机制泛化，避免扩大范围。

Sub2API：

- Header：`Content-Type = application/json`
- 参数：`email = {{username}}`、`password = {{password}}`
- Turnstile：若启用，则补入 `turnstile_token = {{turnstile_token}}`。若用户删除/改名该参数，则以用户模板为准；默认模板恢复时重新生成标准字段。

## NewAPI 鉴权方式

保留三种方式：

1. 账号密码：使用可编辑登录 Header/参数，成功后使用返回 Cookie + User ID。
2. Cookie：沿用现有 `{cookie,user_id}` 凭据格式，兼容旧配置。
3. 访问令牌：新增独立模式，凭据保存长期 token；默认后续请求 Header 为 `Authorization = Bearer {{token}}`。

为了避免把现有 `CredentialMode` 大改为多个数据库枚举，本次将 token 凭据 JSON 增加 `auth_type` 区分：

- 旧 NewAPI JSON 没有 `auth_type` 时按 `cookie` 处理。
- 新 Cookie 模式保存 `{auth_type:"cookie", cookie, user_id}`。
- 访问令牌保存 `{auth_type:"access_token", token, headers:[...]}`，其中 headers 默认包含 Authorization，但用户可增删改。

访问令牌模式不设置伪过期时间，也不走登录刷新；CheckAuth 和实际业务请求直接使用其自定义 Header。

## Sub2API Token 生命周期

Sub2API token 凭据扩展为：

`{access_token, refresh_token, expires_at}`

兼容旧 `{access_token}`：如果没有 refresh_token / expires_at，则仍按旧逻辑尝试使用 access_token；失效时提示用户重新填写完整凭据。

账号密码登录成功后读取并保存：

- `access_token`
- `refresh_token`
- `expires_in`
- 根据当前时间计算 `expires_at`

刷新接口：

`POST /api/v1/auth/refresh`

请求体：`{"refresh_token":"xxx"}`，无需 Authorization Header。响应成功后同时替换 access_token、refresh_token 和 expires_at。

刷新触发采用按需刷新，不新增定时任务：每次 EnsureSession 准备使用 token 前判断剩余有效期。安全窗口取 `min(5 分钟, 原有效期的 10%)`，若无法得到原有效期则使用 5 分钟。进入窗口即先刷新；这样无论后台监控周期如何，都不会依赖单独调度器。

刷新成功后必须把两个 token 和新的 expires_at 一并加密写回凭据，保证进程重启后继续使用最新 refresh_token。刷新失败不覆盖旧凭据，并返回明确错误。

## Connector 与 Service 边界

Connector 负责 HTTP 协议细节：

- 根据 Channel 中解析后的登录模板构造请求。
- NewAPI 根据 session 鉴权类型设置 Cookie/User ID 或自定义 token headers。
- Sub2API 提供 RefreshToken 调用并解析响应。

Channel Service 负责：

- 加解密保存的凭据和登录模板。
- 变量替换前准备 username/password/turnstile 值。
- 判断 Sub2API 是否需要刷新。
- 刷新后原子更新加密凭据。
- 保持旧数据的默认回退逻辑。

## API 与前端

渠道创建/编辑 API 增加：

- `login_headers`
- `login_params`

以数组形式传输，如 `[{"key":"Content-Type","value":"application/json"}]`，便于保留顺序和前端动态编辑。

渠道管理表单：

- 密码模式显示“登录 Headers”和“登录参数”两个动态键值编辑区。
- 支持添加、删除、修改。
- 提供“恢复默认”，按当前渠道类型重置。
- 首次新建自动填入默认值。
- NewAPI token 区新增“Cookie / 访问令牌”子类型；访问令牌模式显示 token 和动态 Header，Authorization 默认 `Bearer {{token}}`。
- Sub2API token 区新增 Refresh Token，并显示/保存过期信息；编辑已有敏感字段仍遵循“留空不修改”。

## 错误处理与并发

- Header/参数 key 为空时忽略空行；同名 key 后项覆盖前项并在前端阻止重复输入。
- refresh_token 缺失时不尝试刷新，按旧 token 兼容路径执行。
- 刷新失败时保留旧 token，错误记录到渠道/账号 last_error。
- 同一渠道可能被多个监控任务并发触发刷新，因此刷新与凭据更新需要以 channel/account session key 为粒度串行化；至少保证数据库最终不会被旧 refresh 响应覆盖新值。

## 测试

后端至少覆盖：

- NewAPI/Sub2API 默认模板生成与变量展开。
- 自定义 Header/参数覆盖默认值。
- 空配置对旧渠道回退默认行为。
- NewAPI 旧 Cookie 凭据兼容与新访问令牌 Header。
- Sub2API 登录解析 refresh_token/expires_in。
- 过期前触发 refresh，成功后 access/refresh token 与 expires_at 同时更新。
- refresh 失败不覆盖旧凭据。
- 旧 Sub2API `{access_token}` 仍可使用。

前端至少通过 TypeScript/Vite 构建，并验证动态键值编辑、恢复默认和三种 NewAPI 鉴权 UI 的状态切换。

## 非目标

- 不把任意 HTTP 方法、登录 URL、响应提取规则做成通用脚本配置。
- 不改变现有 Turnstile Provider 机制。
- 不改变 Relay Station 的管理员 API Key 逻辑。
