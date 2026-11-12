import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { useAuth } from "@/lib/auth-context"
import { SecuritySettingsCard } from "@/components/security-settings-card"

export default function SettingsPage() {
  const { username } = useAuth()
  return (
    <section className="space-y-3">
      <header>
        <h1 className="text-lg font-semibold text-foreground">{"系统设置"}</h1>
        <p className="text-xs text-muted-foreground">
          {"公开页面嵌入策略与账户信息。放行策略保存后对新加载的公开页立即生效。"}
        </p>
      </header>

      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        <SecuritySettingsCard />
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
