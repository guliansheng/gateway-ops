import { BellRing } from "lucide-react"
import { NotificationLogs, NotificationStatus } from "@/components/monitor/bottom-panels"

export default function NotificationsPage() {
  return (
    <section className="space-y-4">
      <header className="flex flex-wrap items-end justify-between gap-3 border-l-2 border-brand pl-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-foreground"><BellRing className="size-5 text-brand" />通知中心</h1>
          <p className="mt-1 text-xs text-muted-foreground">
            {"Telegram / Webhook / 邮件 / 企业微信 / 钉钉 / 飞书 / Bark。每个渠道可单独订阅指定上游和分组。"}
          </p>
        </div>
      </header>
      <div className="space-y-4">
        <NotificationStatus fullPage />
        <NotificationLogs />
      </div>
    </section>
  )
}
