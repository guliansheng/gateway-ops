# GatewayOps

GatewayOps（AI 网关运营中枢）是面向 NewAPI / Sub2API 生态的自托管运营管理平台，集中管理渠道监控、倍率变化、成本核算、中转站账号、通知和运营账本。

## 功能

### 渠道管理

- 支持 NewAPI 和 Sub2API 渠道。
- 支持账号密码和 Token 凭据模式。
- 自动或手动维护余额，设置余额阈值和监控开关。
- 自动读取分组倍率，也支持手工维护倍率和分组。
- 登录测试、余额刷新、倍率刷新、完整同步、余额历史和倍率变化记录。
- 支持 Turnstile 和多种打码 provider。

### 运营总览

- 渠道余额、倍率变化、低余额和同步状态总览。
- 24 小时、7 天、30 天等区间统计。
- 中转站数量、账号风险、成本和用户扣费汇总。
- 最近倍率变化和自动调组记录。

### 中转站管理

中转站管理当前按 Sub2API 管理 API 实现，支持：

- 账号、分组、渠道和用户快照同步。
- 独立的倍率探测和快照同步计划。
- API Key 成本倍率、分组倍率、账号消费和用户扣费统计。
- 账号分组、模型类型、调度、优先级、并发和池模式设置。
- 账号风险识别、自动调组、自动降级和优先级回调。
- 公开 / 专属分组、分组快速测试、账号连接测试和公共分组监控。
- 用户状态、并发限额和余额历史管理。

> NewAPI 渠道可以在“渠道管理”中使用，但“中转站管理”目前不兼容 NewAPI。

### 运营与通知

- 成本管理：收入、支出、备注和中转站关联，自动汇总中转站用户实际扣费。
- 本地号池：账号登记、成本、状态、自动关联和 OAuth 号池统计。
- 通知方式：Telegram、Webhook、SMTP 邮件、企业微信、钉钉、飞书和 Bark。
- 通知规则可按渠道或指定分组的倍率变化筛选。

## 页面

- **首页**：运营总览、渠道健康、倍率变化、中转站统计和调整记录。
- **渠道管理**：NewAPI / Sub2API 渠道、余额、倍率和同步任务。
- **中转站管理**：Sub2API 中转站、账号、分组、用户和同步计划。
- **运营管理 / 成本管理**：收入支出账本和区间经营汇总。
- **运营管理 / 本地号池**：本地账号和 OAuth 号池统计。
- **系统设置 / 验证码服务**：Turnstile 打码 provider。
- **系统设置 / 通知渠道**：通知渠道、测试发送和订阅规则。

## Docker Compose 部署

### 准备配置

```bash
cp .env.example .env
```

至少修改：

```env
GATEWAYOPS_DATABASE_USER=gatewayops
GATEWAYOPS_DATABASE_PASSWORD=请替换为数据库密码
GATEWAYOPS_DATABASE_NAME=gatewayops
APP_SECRET=请替换为 32 字节以上随机字符串
```

`APP_SECRET` 用于加密渠道凭据、通知密钥和中转站管理员 API Key。丢失或更换后旧数据无法解密，必须长期保存。

公网部署必须开启登录：

```env
AUTH_ENABLED=true
ADMIN_USERNAME=admin
ADMIN_PASSWORD=请替换为强密码
```

当前 Compose 会接入 Sub2API 共享网络。网络不存在时先执行：

```bash
docker network create sub2api-deploy_sub2api-network
```

启动：

```bash
docker compose up -d --build
```

访问 `http://127.0.0.1:8080`，检查健康状态：

```bash
curl http://127.0.0.1:8080/healthz
```

预期返回 `{"status":"ok"}`。

### 常用命令

```bash
docker compose ps
docker compose logs -f app
docker compose up -d --build app
docker compose stop
```

PostgreSQL 数据保存在 Compose 卷 `gatewayops-postgres-data` 中。不要在没有备份的情况下删除该卷。

## 环境变量

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `GATEWAYOPS_DATABASE_USER` | `gatewayops` | PostgreSQL 用户 |
| `GATEWAYOPS_DATABASE_PASSWORD` | `change-me` | PostgreSQL 密码，生产环境必须修改 |
| `GATEWAYOPS_DATABASE_NAME` | `gatewayops` | PostgreSQL 数据库名 |
| `GATEWAYOPS_POSTGRES_PORT` | `54329` | 宿主机 PostgreSQL 端口 |
| `GATEWAYOPS_HTTP_PORT` | `8080` | 宿主机 Web 端口 |
| `GATEWAYOPS_IMAGE_TAG` | `latest` | 镜像标签 |
| `GATEWAYOPS_SERVER_MODE` | `release` | 后端运行模式 |
| `GATEWAYOPS_LOG_LEVEL` | `info` | 日志等级 |
| `APP_SECRET` | 无 | 敏感数据加密主密钥，必填 |
| `AUTH_ENABLED` | `false` | 是否开启后台登录 |
| `ADMIN_USERNAME` | `admin` | 后台管理员账号 |
| `ADMIN_PASSWORD` | 空 | 后台管理员密码 |
| `AUTH_TOKEN_SECRET` | 空 | Token 密钥，空时回退到 `APP_SECRET` |

部署只使用 `GATEWAYOPS_*` 配置名，Compose 项目、容器和数据库均采用 GatewayOps 命名。

## 添加渠道

在“渠道管理”点击“添加渠道”：

- **NewAPI**：使用 NewAPI 登录接口、Cookie / User ID Token 或账号密码。
- **Sub2API**：使用 Sub2API 登录接口、Access Token 或账号密码。

自动余额模式需要目标站点允许登录并读取余额；无法自动读取时使用手动余额。自动倍率模式读取站点可见分组倍率，无法读取时可以手工创建分组和倍率。

启用 Turnstile 的站点，需要先在“验证码服务”创建并测试 provider，再在渠道配置中绑定。

## 添加中转站

在“中转站管理”填写名称、Sub2API 地址和管理员 API Key，然后点击“实时同步”。管理员 API Key 需要有对应管理权限。

分组快速测试需要已启用且绑定当前分组的管理员 API Key，或未绑定分组的全局管理员 API Key。

- **倍率探测**：读取 API Key 的上游成本倍率，用于成本统计、利润判断和自动调组。
- **快照同步**：刷新账号、分组和关联关系，不执行倍率探测。

## 通知订阅规则

留空表示接收全部事件，也可以限制渠道或分组：

```json
[
  { "channel_id": 1, "mode": "all", "groups": [] },
  { "channel_id": 2, "mode": "groups", "groups": ["cc-max", "codex"] }
]
```

`channel_id` 是渠道 ID；`mode=all` 接收该渠道全部事件；`mode=groups` 的倍率变化只匹配 `groups` 中的分组名称，其他事件仍按渠道匹配。

## 安全和数据

- 备份并妥善保存 `APP_SECRET`。
- 生产环境不要在未开启登录时暴露公网。
- 管理员 API Key 只在后端加密保存，不返回前端。
- 删除中转站会删除本地账号、分组、渠道快照和调整配置，但保留历史运营账本记录。

## 技术结构

- Backend：Go 1.23、Gin、GORM、PostgreSQL。
- Frontend：React 19、Vite、TypeScript、Tailwind CSS、Radix UI。
- 前端构建产物嵌入 Go 二进制，由单个应用容器提供页面和 API。
- 数据库：PostgreSQL 16；定时任务由后端调度器执行。

## License

GatewayOps is licensed under the [GNU Affero General Public License v3.0](./LICENSE).

This project is a modified derivative work based on the MIT-licensed upstream project `worryzyy/upstream-hub`. The upstream copyright and license text are retained in [LICENSES/MIT-upstream.txt](./LICENSES/MIT-upstream.txt), and the attribution details are documented in [NOTICE.md](./NOTICE.md).
