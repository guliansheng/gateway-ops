import { useEffect, useMemo, useState } from "react"
import { useTheme } from "next-themes"
import { Activity, Bell, ChevronDown, Github, Home, LayoutList, LogOut, RefreshCw, Server, Settings2, ShieldCheck, Sun, Moon, WalletCards, UsersRound } from "lucide-react"
import { Link, NavLink, useLocation } from "react-router-dom"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { useAuth } from "@/lib/auth-context"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { useChannels } from "@/lib/queries"
import { relativeTime } from "@/lib/format"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"

export function MonitorHeader() {
  const { theme, setTheme } = useTheme()
  const { username, authDisabled, logout } = useAuth()
  const refresh = useTriggerRefresh()
  const channels = useChannels()
  const location = useLocation()
  const [mounted, setMounted] = useState(false)
  const [syncing, setSyncing] = useState(false)

  useEffect(() => setMounted(true), [])

  /**
   * 找出所有渠道中最近一次采集时间——这是"上次采集"展示的依据，
   * 让用户知道页面上的余额到底是多新的快照（区别于"我刚点了刷新"）。
   */
  const lastCollectedAt = useMemo(() => {
    const list = channels.data ?? []
    let best: string | null = null
    let bestT = -Infinity
    for (const c of list) {
      if (!c.last_balance_at) continue
      const t = new Date(c.last_balance_at).getTime()
      if (Number.isFinite(t) && t > bestT) {
        bestT = t
        best = c.last_balance_at
      }
    }
    return best
  }, [channels.data])

  function handleRefresh() {
    setSyncing(true)
    refresh()
    setTimeout(() => setSyncing(false), 800)
  }

  const navItems = [
    { to: "/", label: "首页", icon: Home, end: true },
    { to: "/channels", label: "渠道管理", icon: LayoutList },
    { to: "/relay-stations", label: "中转站管理", icon: Server },
    { to: "/public/service-status", label: "服务状态", icon: Activity },
  ]

  const operationItems = [
    { to: "/operations/costs", label: "成本管理", icon: WalletCards },
    { to: "/operations/local-pool", label: "本地号池", icon: UsersRound },
  ]
  const operationActive = location.pathname.startsWith("/operations/")
  const systemItems = [
    { to: "/captcha", label: "验证码服务", icon: ShieldCheck },
    { to: "/notifications", label: "通知渠道", icon: Bell },
  ]
  const systemActive = ["/captcha", "/notifications"].some((path) => location.pathname.startsWith(path))
  const isPathActive = (to: string, end = false) => end ? location.pathname === to : location.pathname.startsWith(to)

  return (
    <header className="sticky top-0 z-20 border-b border-border bg-background/95 backdrop-blur-sm">
      <div className="flex h-14 w-full items-center justify-between gap-2 px-6 sm:gap-4">
        {/* left: logo + title */}
        <div className="flex min-w-0 items-center gap-2.5">
          <div className="flex size-8 items-center justify-center rounded-lg bg-foreground text-background">
            <Activity className="size-4" strokeWidth={2.5} />
          </div>
          <h1 className="truncate text-base font-semibold tracking-tight text-foreground"><span>GatewayOps</span></h1>
        </div>

        <nav className="hidden items-center gap-1 lg:flex" aria-label="主菜单">
          {navItems.map((item) => (
            item.to === "/public/service-status" ? (
              <a
                key={item.to}
                href={item.to}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex h-9 items-center gap-1.5 rounded-md px-3 text-sm text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
              >
                <item.icon className="size-3.5" />
                {item.label}
              </a>
            ) : (
              <NavLink
                key={item.to}
                to={item.to}
                end={item.end}
                className={({ isActive }) => cn(
                  "inline-flex h-9 items-center gap-1.5 rounded-md px-3 text-sm transition-colors",
                  isActive ? "bg-muted font-medium text-foreground" : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                )}
              >
                <item.icon className="size-3.5" />
                {item.label}
              </NavLink>
            )
          ))}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button type="button" className={cn("inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-md px-3 text-sm transition-colors hover:bg-muted/60 hover:text-foreground", operationActive ? "bg-muted font-medium text-foreground" : "text-muted-foreground")} aria-label="运营管理菜单">
                <WalletCards className="size-3.5" />运营管理<ChevronDown className="size-3" />
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-40">
              <DropdownMenuLabel>运营管理</DropdownMenuLabel>
              <DropdownMenuSeparator />
              {operationItems.map((item) => <DropdownMenuItem key={item.to} asChild><Link to={item.to}><item.icon />{item.label}</Link></DropdownMenuItem>)}
            </DropdownMenuContent>
          </DropdownMenu>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button type="button" className={cn("inline-flex h-9 cursor-pointer items-center gap-1.5 rounded-md px-3 text-sm transition-colors hover:bg-muted/60 hover:text-foreground", systemActive ? "bg-muted font-medium text-foreground" : "text-muted-foreground")} aria-label="系统设置菜单"><Settings2 className="size-3.5" />系统设置<ChevronDown className="size-3" /></button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="w-44"><DropdownMenuLabel>系统设置</DropdownMenuLabel><DropdownMenuSeparator />{systemItems.map((item) => <DropdownMenuItem key={item.to} asChild><Link to={item.to}><item.icon />{item.label}</Link></DropdownMenuItem>)}</DropdownMenuContent>
          </DropdownMenu>
        </nav>

        {/* right: actions */}
        <div className="flex shrink-0 items-center gap-1 sm:gap-3 xl:mr-[120px]">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon" className="size-11 lg:hidden sm:size-8" aria-label="打开菜单"><LayoutList className="size-4" /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" sideOffset={8} className="w-[min(20rem,calc(100vw-2rem))] p-2">
              <DropdownMenuLabel className="flex items-center gap-2 px-2 py-2 text-xs font-semibold text-foreground">
                <LayoutList className="size-4 text-brand" />主菜单
              </DropdownMenuLabel>
              <div className="space-y-0.5">
                {navItems.map((item) => {
                  const active = isPathActive(item.to, item.end)
                  const content = item.to === "/public/service-status"
                    ? <a href={item.to} target="_blank" rel="noopener noreferrer"><item.icon className={cn(active && "text-brand")} />{item.label}{active ? <span className="ml-auto size-1.5 rounded-full bg-brand" /> : null}</a>
                    : <Link to={item.to}><item.icon className={cn(active && "text-brand")} />{item.label}{active ? <span className="ml-auto size-1.5 rounded-full bg-brand" /> : null}</Link>
                  return <DropdownMenuItem key={item.to} asChild className={cn("min-h-11 rounded-md px-3 sm:min-h-9", active && "bg-brand/10 font-medium text-brand focus:bg-brand/10 focus:text-brand")}>{content}</DropdownMenuItem>
                })}
              </div>

              <DropdownMenuSeparator className="my-2" />
              <DropdownMenuLabel className="flex items-center gap-2 px-2 py-1.5 text-xs font-semibold text-foreground">
                <span className="flex size-7 items-center justify-center rounded-md bg-brand/10 text-brand"><WalletCards className="size-3.5" /></span>
                运营管理
              </DropdownMenuLabel>
              <div className="ml-3 space-y-0.5 border-l border-border pl-3">
                {operationItems.map((item) => {
                  const active = isPathActive(item.to)
                  return <DropdownMenuItem key={item.to} asChild className={cn("min-h-11 rounded-md px-3 sm:min-h-9", active && "bg-muted font-medium text-foreground")}><Link to={item.to}><item.icon />{item.label}{active ? <span className="ml-auto text-[10px] font-medium text-brand">当前</span> : null}</Link></DropdownMenuItem>
                })}
              </div>

              <DropdownMenuLabel className="mt-2 flex items-center gap-2 px-2 py-1.5 text-xs font-semibold text-foreground">
                <span className="flex size-7 items-center justify-center rounded-md bg-muted text-foreground"><Settings2 className="size-3.5" /></span>
                系统设置
              </DropdownMenuLabel>
              <div className="ml-3 space-y-0.5 border-l border-border pl-3">
                {systemItems.map((item) => {
                  const active = isPathActive(item.to)
                  return <DropdownMenuItem key={item.to} asChild className={cn("min-h-11 rounded-md px-3 sm:min-h-9", active && "bg-muted font-medium text-foreground")}><Link to={item.to}><item.icon />{item.label}{active ? <span className="ml-auto text-[10px] font-medium text-brand">当前</span> : null}</Link></DropdownMenuItem>
                })}
              </div>

              <DropdownMenuSeparator className="sm:hidden" />
              <DropdownMenuItem className="min-h-11 sm:hidden" onSelect={() => setTheme(theme === "dark" ? "light" : "dark")}>
                {mounted && theme === "dark" ? <Moon /> : <Sun />}{mounted && theme === "dark" ? "切换浅色模式" : "切换深色模式"}
              </DropdownMenuItem>
              <DropdownMenuItem asChild className="min-h-11 sm:hidden"><a href="https://github.com/guliansheng/gateway-ops" target="_blank" rel="noopener noreferrer"><Github />{"GitHub 仓库"}</a></DropdownMenuItem>
              {authDisabled ? null : <DropdownMenuItem className="min-h-11 text-danger sm:hidden" onSelect={logout}><LogOut />{"退出登录"}</DropdownMenuItem>}
            </DropdownMenuContent>
          </DropdownMenu>

          {/* last collected + refresh */}
          <div className="hidden items-center gap-2 sm:flex">
            <span className="text-xs text-muted-foreground">
              {"上次采集 "}
              <span className="font-medium text-foreground">{relativeTime(lastCollectedAt)}</span>
            </span>
            <Tooltip delayDuration={200}>
              <TooltipTrigger asChild>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={handleRefresh}
                  disabled={syncing}
                  className="gap-1.5 border-border bg-background text-foreground hover:bg-muted"
                  aria-label="刷新视图"
                >
                  <RefreshCw className={cn("size-3.5", syncing && "animate-spin")} />
                  {"刷新视图"}
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom" className="max-w-xs text-xs">
                <p>{"重新拉取最新的快照数据。"}</p>
                <p className="mt-1 text-muted-foreground">
                  {"提示：实际采集由后台定时任务执行，如需立即采集请到具体渠道点 \"同步\"。"}
                </p>
              </TooltipContent>
            </Tooltip>
          </div>

          {/* mobile-only refresh (no tooltip / no timestamp to save space) */}
          <Button
            variant="outline"
            size="icon"
            onClick={handleRefresh}
            disabled={syncing}
            className="size-11 border-border bg-background text-foreground hover:bg-muted sm:hidden"
            aria-label="刷新视图"
          >
            <RefreshCw className={cn("size-4", syncing && "animate-spin")} />
          </Button>

          {/* GitHub repo link */}
          <Tooltip delayDuration={200}>
            <TooltipTrigger asChild>
              <Button
                asChild
                variant="outline"
                size="icon"
                className="hidden size-8 border-border bg-background text-foreground hover:bg-muted sm:inline-flex"
                aria-label="GitHub 仓库"
              >
                <a
                  href="https://github.com/guliansheng/gateway-ops"
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  <Github className="size-3.5" />
                </a>
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom" className="text-xs">
              {"GitHub 仓库"}
            </TooltipContent>
          </Tooltip>

          {/* theme toggle */}
          <Tooltip delayDuration={200}>
            <TooltipTrigger asChild>
              <Button
                variant="outline"
                size="icon"
                onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
                className="hidden size-8 border-border bg-background text-foreground hover:bg-muted sm:inline-flex"
                aria-label="切换主题"
              >
                {mounted && theme === "dark" ? (
                  <Moon className="size-3.5" />
                ) : (
                  <Sun className="size-3.5" />
                )}
              </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom" className="text-xs">
              {mounted && theme === "dark" ? "深色模式 · 点击切换浅色" : "浅色模式 · 点击切换深色"}
            </TooltipContent>
          </Tooltip>

          {/* logout — 鉴权关闭时整个按钮不显示 */}
          {authDisabled ? null : (
            <Tooltip delayDuration={200}>
              <TooltipTrigger asChild>
                <Button
                  variant="outline"
                  size="icon"
                  onClick={logout}
                  className="hidden size-8 border-border bg-background text-foreground hover:bg-muted sm:inline-flex"
                  aria-label="退出登录"
                >
                  <LogOut className="size-3.5" />
                </Button>
              </TooltipTrigger>
              <TooltipContent side="bottom" className="text-xs">
                {username ? `${username} · 退出登录` : "退出登录"}
              </TooltipContent>
            </Tooltip>
          )}
        </div>
      </div>
    </header>
  )
}
