import { ShieldCheck } from "lucide-react"
import { CaptchaStatus } from "@/components/monitor/bottom-panels"

export default function CaptchaPage() {
  return (
    <section className="space-y-4">
      <header className="flex flex-wrap items-end justify-between gap-3 border-l-2 border-brand pl-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold text-foreground"><ShieldCheck className="size-5 text-brand" />验证码服务</h1>
          <p className="mt-1 text-xs text-muted-foreground">
            {"配置 CapSolver、2Captcha 等服务，为启用 Turnstile 的渠道自动获取验证令牌。"}
          </p>
        </div>
      </header>
      <CaptchaStatus fullPage />
    </section>
  )
}
