import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { useAuth } from "@/lib/auth-context"

export default function SettingsPage() {
  const { username } = useAuth()
  return (
    <section className="space-y-3">
      <header>
        <h1 className="text-lg font-semibold text-foreground">{"系统设置"}</h1>
        <p className="text-xs text-muted-foreground">
          {"账户信息与运行参数。修改 cron / 数据库 / 加密密钥需要重启后端。"}
        </p>
      </header>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">{"当前账户"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"管理员"}</span>
              <span className="font-medium">{username ?? "—"}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">
              {"账号 / 密码写在 backend/config.yaml 的 auth 段，或 ADMIN_USERNAME / ADMIN_PASSWORD 环境变量。"}
            </p>
          </CardContent>
        </Card>

        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">{"调度计划"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"余额扫描"}</span>
              <span className="font-medium">{"每 15 分钟"}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"倍率扫描"}</span>
              <span className="font-medium">{"每 30 分钟"}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">
              {"修改 backend/config.yaml 的 scheduler 段，重启后端生效。"}
            </p>
          </CardContent>
        </Card>

        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">{"前端轮询"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"自动刷新"}</span>
              <span className="font-medium">{"30 秒"}</span>
            </div>
            <p className="text-[11px] text-muted-foreground">
              {"页面在后台标签时暂停轮询，回到前台立即触发一次。手动点头部的[刷新]立即拉。"}
            </p>
          </CardContent>
        </Card>

        <Card className="border border-border shadow-none">
          <CardHeader className="pb-2">
            <CardTitle className="text-base font-semibold">{"关于"}</CardTitle>
          </CardHeader>
          <CardContent className="space-y-2 text-sm">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"项目"}</span>
              <span className="font-medium">{"GatewayOps"}</span>
            </div>
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">{"版本"}</span>
              <span className="font-medium">{"0.1.0-dev"}</span>
            </div>
          </CardContent>
        </Card>
      </div>
    </section>
  )
}
