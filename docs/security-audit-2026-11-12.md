# GatewayOps 安全审计与加固记录

日期：2026-11-12（Asia/Shanghai）

范围：当前源码、构建依赖、应用镜像、实际反代配置，以及 `https://gatewayops.qidian7.de5.net` 的低频非破坏性验证。

## 结论与边界

本次审计发现并修复了匿名邮箱枚举、可信上游重定向导致凭据第二跳外传、无界上游响应、通知错误副本泄密等问题。管理 API 的匿名与伪造令牌验证均未绕过登录；未证明存在匿名远程代码执行、SQL 注入或任意文件读取。

不做密码爆破、不读取真实业务列表或用户邮箱、不调用真实付费检测/打码、不向真实外站传输密钥。凭据外传和资源放大用本地 `httptest`、假配置和假密钥复现。公开冷缓存压力用本地并发屏障验证，而非对生产站压测。

**这不是“以后绝对攻不破”的保证，也不是历史入侵取证。** 未全面调查既往访问日志，不能据此断言此前没有被利用；若站点曾使用来源不可信的集成端点，应考虑轮换相关管理密钥、密码、刷新令牌和 Webhook 凭据。

## 发现与修复

### 1. 高风险：匿名用户 ID 枚举邮箱

旧 `GET /api/public/relay-stations/:id/help/commenter?user_id=...` 使用后端保存的站点管理权限查询邮箱并原样返回。攻击者不需要登录 GatewayOps 即可枚举已知或猜测的用户 ID。

修复：保留旧响应协议，但恒返回 `{"data":{"author_name":""}}`，既不查询数据库身份信息，也不调用上游。不同站点/用户 ID 返回相同空值，且 `Cache-Control: no-store`；不只做邮箱打码，避免继续泄漏用户存在性。访客自行填写留言名称。

### 2. 高风险（有前提）：上游重定向重放凭据

旧 relay 的 Go 客户端及其它 Resty 客户端默认跟随重定向。Go 会在部分跨主机重定向中删除标准 `Authorization`/`Cookie`，但不自动保护任意 `x-api-key`；同主机跨端口仍可能保留标准认证头。307/308 还可重放携带密码、刷新令牌或打码 `clientKey` 的 JSON 请求体。

前提：管理员已经配置的可信上游被控制、失陷、错误重定向或存在开放跳转。**没有证明匿名用户能够任意设置集成 URL**，不将管理员主动配置合法内网地址泛报为匿名 SSRF。

修复：relay 共享客户端使用 `http.ErrUseLastResponse`；NewAPI/Sub2API 连接器、四家打码、六个 HTTP 通知集成统一使用 `internal/httpclient.New()`，拒绝自动重定向，不访问第二跳。保留既有超时、UA 等业务设置。集成若依赖跳转，应直接配置最终地址。独立的降智 HTTP 客户端原已禁止跳转，保持不变。

验证覆盖 301/302/303/307/308、同主机跨端口/不同主机、敏感头与敏感请求体，均仅使用本地假数据。

### 3. 中风险：无解压后响应上限及错误膨胀

旧部分 relay JSON Decoder 与 Resty 响应读取没有字节上限。本地旧行为可将 8,261 字节 gzip 膨胀为 8,388,692 字节，业务错误还能携带数 MiB 上游文本进入监控日志；超时不能代替字节上限。

修复：普通 relay JSON 上限 16 MiB，读取最多上限加一并拒绝超大尾随数据；共享 Resty 工厂在解压后的 body 读取阶段限制 16 MiB，不是无界读取后截断。正常大列表仍允许在上限内返回。API 请求体统一 8 MiB；公开留言 16 KiB；定长及 chunked 超限均有回归。

### 4. 中风险：通知错误副本泄露 Webhook 密钥

旧 `url.Error` 可包含完整 Webhook 路径、`access_token` 查询或签名，经过重试日志、通知日志数据库及测试发送 API，绕过配置加密保存机制。

修复：所有通过 `Build` 返回的通知器统一包装。传输 URL 错误转换为通用失败/超时；其它错误隐藏配置中的字符串值及 URL，并限制错误长度；在进入 Dispatcher 重试、落库及 API 前完成。保留 `context.Canceled` / `DeadlineExceeded` 语义。此措施不会清理历史日志中可能已有的副本。

### 5. 中风险：公开接口边界与冷缓存并发

- 帮助文档的历史 `station_url` 可能包含 `https://user:password@host`，公开返回会暴露 URL 身份信息。新配置拒绝 URL userinfo；历史值的公开输出也剥离 userinfo，避免只修输入不保护存量。
- 公开模型定价在全部上游探测失败时，旧实现将任意上游错误文本直接返回匿名访客。改为通用公开错误，详情仅服务端记录；不假定每次上游错误都含真实密钥。
- 公开分组监控缓存 miss 原可每请求重新启动 6 个 worker，按组/分页发出约 `2 × G × P` 个请求（`P ≤ 50`）；公开服务状态也会重复聚合。同键冷缓存请求合并，闭包内重查缓存，不持缓存锁做 DB/HTTP。保留既有 TTL 和超时边界。此措施减少重复工作，但不是所有不同键请求或分布式 DoS 的完整防护。

### 6. 中风险：匿名留言刷屏与入口资源滥用

公开留言新增每 IP 每分钟 5 次、每进程每分钟 60 次尝试的有界计数，非法请求也计数，超过返回 429 + `Retry-After`，计数表满时拒绝新增键。

本机 nginx：普通请求 10 次/秒、burst 60；登录 10 次/分钟、burst 5；全站 body 8 MiB、登录 16 KiB；请求体/头超时。Go 增加读取 30 秒、空闲 90 秒、头 64 KiB 上限；不加破坏 SSE 的全局写超时。反代保留 SSE 不缓冲。

### 7. 代理信任与容器降权

nginx 只信已列出的 Cloudflare 地址，并用解析后的 `$remote_addr` 覆盖而非追加 `X-Forwarded-For`。应用只信本机反代实际经过的回环地址和网桥 `172.18.0.1/32`、`172.19.0.1/32`，不再信整个私网；伪造 XFF 不会重置留言配额。

应用容器非 root，新增 `cap_drop: ALL`、`no-new-privileges:true`；数据库容器和数据卷不重建。应用响应加入 nosniff、严格 Referrer-Policy、Permissions-Policy、API no-store 与最小 CSP；这份 CSP 不是完整的脚本白名单。

## iframe 误伤修复与可配置放行

第一轮加固全站设置 `X-Frame-Options: DENY` / `frame-ancestors 'none'`，误伤了 Sub2API 内嵌公开页。本轮将策略改为明确范围的可持久化白名单，而不是删除全站防护。

管理员入口：**系统设置 → 安全与运行设置 → 公开页面 iframe 放行策略**，路径 `/settings`。每行一个完整 HTTPS 来源。为本站配置 `https://qidian7.top`。

- 仅 `/public/relay-monitor/:正整数`、`/public/model-pricing/:正整数`、`/public/service-status`、`/public/help/:正整数` 文档使用白名单 CSP；这些文档不发送冲突的 XFO。其它页面、登录和 API 继续 `DENY` / `'none'`。
- 来源精确匹配，不隐含子域名或非默认端口，不接受通配符、路径、HTTP、userinfo、CRLF 或 CSP 指令；最多 20 个。使用 CSP `frame-ancestors`，不用已失效的 XFO `ALLOW-FROM`。公开文档设置 `Cache-Control: no-store`，避免策略更新后仍使用旧白名单或缓存的 XFO。
- 设置 API 仅在认证组：`GET/PUT /api/security-settings`。保存先成功持久化再激活；持久化不持读锁阻塞公开页。清空数组撤销全部放行；未配置、配置损坏或读取失败均默认拒绝。
- 存入现有 `app_settings` 的 `security.public_frame_origins`，无新增 schema 迁移。单实例即时更新；多实例需同步重载或重启其它实例。
- 公开 SPA 如果客户端导航到后台路径，前端在挂载认证提供器前拒绝 iframe 内的管理界面，避免沿用公开文档 CSP 进入后台；完整后台文档请求仍由服务端 CSP 拦截。
- 不开放 CORS，不放宽认证，不使用请求 `Origin` / `Referer` 动态拼接策略；多层 iframe 的所有祖先都必须符合白名单。
- 只读检查发现 Sub2API `https://qidian7.top` 的自身 `frame-src` 已包含 GatewayOps 域名，因此无需降低它的 CSP。

## 依赖与构建

- 后端 Go 1.26（镜像工具链 1.26.11）；`x/net v0.62.0`、`x/crypto v0.59.0`、`x/text v0.44.0`、`pgx/v5 v5.9.2`。第一轮扫描曾命中旧 x/net、x/text、pgx 可达公告；之后也更新了仅包/模块层不可达公告对应版本。最终结果以本轮全量扫描为准。
- 前端删除未使用的 zod / hookform resolver，更新 Vite、React Router 和受影响传递依赖；锁文件冻结安装。`pnpm audit` 由 10 high + 10 moderate + 1 low 降为无已知漏洞。
- 构建 Node 24，Go 1.26.11，运行基础镜像 Alpine 3.23。前端仍有大 bundle 警告，非本轮已确认漏洞，不为安全修复做无关大迁移。

## 测试与上线验收

第二轮已完成并上线，以下命令/测试均退出 0：

- 后端 `go test -count=1 ./...`：全部包通过；`govulncheck -show verbose ./...` 扫描 39 个模块与 Go 1.26.11 标准库，所有扫描层级均 `No vulnerabilities found`，不只是可达符号为零。
- 同键缓存冷启动并发测试各重复 25 次通过；覆盖合并为一次加载、返回结果深拷贝；iframe 策略覆盖持久化、失败关闭、撤销、注入拒绝、匿名 GET/PUT 401 以及慢存储不阻塞公开页读取。
- 前端 `pnpm audit` 无已知漏洞，`tsc --noEmit -p tsconfig.json` 与 `pnpm build` 成功。
- 最终镜像使用 Trivy 0.76.2、仅漏洞扫描（非 secret 扫描），扫描实际导出镜像的 Alpine 3.23.8（22 包）和 Go 二进制，**各级别已知漏洞均为 0**。初次镜像扫描发现中风险 `CVE-2026-4738`，将 zlib `1.3.2-r1` 更新到 `1.4-r0` 后重建复扫通过；未忽略该公告。扫描库于当日下载，不代表未来公告也为零。
- 线上四类公开文档均 200、CSP 精确允许 `https://qidian7.top`、无冲突 XFO、no-store。根页面/设置页面继续禁止嵌入；匿名设置 GET/PUT 和伪造 XFF 请求均 401；敏感文件路径仍 404。
- Chromium 使用**真实生产子页面 HTML/资源/响应头**，仅合成父来源文档和 mock API：允许 qidian7.top → 公开服务状态；拒绝其它来源 → 公开页；拒绝 qidian7.top → 根页面/设置；拒绝未放行的外层祖先；公开 iframe 经 SPA 导航不能挂载后台认证界面。设置表单读取/保存/刷新也通过，但写入仅发送到 mock API，不是生产写入。未登录 Sub2API 或更改其业务数据；检查其公开响应头确认已有必要 frame-src。
- 最终 JS `index-C722YWeB.js` SHA256：`ea0b81c0cb92d91d48aa2455703e5b629aae2002db82788c034e7a06bb59ade6`；CSS `index-DheXvEVG.css` SHA256：`a7a276a310b86df37e22fc6413d64082c4c3a1a3791505a766cab20db1b7aff4`。公网上下载资源与本地构建逐字一致。
- 新应用 `healthy`、healthz 为 `{"status":"ok"}`，保留 `ALL` capability 丢弃和 no-new-privileges；原数据库容器 ID、数据卷及挂载未变化。只新增用户要求的 iframe 配置项，未执行新增 schema 迁移或重建数据库；未做逐行数据一致性校验。

已执行的第一轮线上只读验证：根页面和 `/healthz` 200；匿名管理 API、无效 Bearer 及伪造 XFF 仍 401；旧 commenter 不同站点/用户 ID 恒空；`/.env`、`/.git/config` 404。部署 JS/CSS SHA256 与本地产物一致；实际 Chromium 显示登录页、无脚本异常，未提交登录表单。本机站点 ID 0 的六次无效留言得到 `[400,400,400,400,400,429]`，无留言写入；超大 `Content-Length` 在 nginx 与应用均 413。

## 部署、备份与回退

仅替换应用镜像，沿用原端口、凭据、数据库和数据卷。第一轮镜像 `gatewayops:security-20261112T1000Z`；第二轮已上线镜像 **`gatewayops:security-frame-20261112T1137Z`**，config digest `sha256:04ced724ef6a17d24184076da99d3b48b8a99b0923789245ab9391a6de6b216e`。应用 Compose 以容器自身标签为准，使用根配置和 `backups/docker-compose.local.yml`，`up -d --no-deps --no-build --pull never --wait ... app`，不执行 `down -v`。本次源码修改尚未 Git 提交或推送。

第一轮配置备份在 `backups/security-20261112T1005Z`；第二轮切换前配置和 iframe 单项策略备份在 `backups/security-iframe-20261112T1120Z`。**这些是配置/单项策略备份，不是数据库备份。** 旧镜像保留可回退；回退旧镜像会重新失去新修复及 iframe 放行能力。需撤销嵌入时优先在新版本设置中清空白名单，无需回退全部安全补丁。

第二轮前原 iframe 配置项不存在（备份为 `[]`）。若确需恢复该单项初始状态，应仅删除 `security.public_frame_origins` 并重启应用，不要还原/删除其它设置或数据。仅恢复旧镜像时可保留此项，它不会被旧版本使用。

原数据库容器基线 ID：`5a3f9d9e0020c56dba1c42b3b562a4fe45a2a4cf8a2388f7f44ce46802126596`；数据卷 `gatewayops-postgres-data`，宿主挂载 `/var/lib/docker/volumes/gatewayops-postgres-data/_data`。部署后核对 ID/挂载与健康状态；这不等同于逐行数据一致性校验。

## 剩余风险与建议

1. 管理面仍是公网单管理员密码入口。建议再加 Cloudflare Access/SSO 或管理网段限制，同时为公开页/公开 API 保留必要匿名访问；不能直接把整个域名关进登录墙而再次误伤嵌入。
2. 限流、缓存及 singleflight 为单进程；大规模分布式流量、不同有效缓存键和多实例需要 CDN/WAF、共享配额和容量规划。DoS/付费消耗攻击没有在生产做压力测试。
3. 本产品需访问管理员配置的内网服务，因此未一刀切禁止所有私网目标。若多人管理或上游不可信，应进一步做按集成目标的出站网络白名单；禁止重定向不等于覆盖所有 SSRF/DNS 攻击。
4. 富文本服务端清洗弱于前端允许列表。当前前端重复清洗、srcdoc nonce CSP、无 `allow-same-origin` 沙箱仍必须保留；本轮未证明同源 XSS，但未来若去掉沙箱或换渲染方式需重新审计。
5. 登录 token 使用 sessionStorage、有效期内无集中撤销，仍需防同源 XSS。修改签名密钥会使全部旧令牌失效，必须按运维流程操作。
6. 通知历史日志可能仍有旧凭据副本，新脱敏不追溯清理。应按实际暴露范围评估日志访问权限、保留期和凭据轮换，不在不了解业务数据的情况下批量删除。
7. OS/镜像与应用依赖需要定期复扫；某次“无已知漏洞”只对应当时公告库和扫描范围，不能覆盖未来漏洞、全部业务逻辑或第三方服务自身安全。
