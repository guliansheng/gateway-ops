"use client"

import type { ReactNode } from "react"
import { RefreshCw, type LucideIcon } from "lucide-react"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

interface PublicPageHeaderProps {
  icon: LucideIcon
  title: string
  description: ReactNode
  controls?: ReactNode
  metadata?: ReactNode
  updatedAt?: string | null
  refreshing?: boolean
  refreshIn?: number
  refreshLabel?: string
  onRefresh?: () => void
}

function updatedTime(value?: string | null, format: "short" | "full" | "time" = "short") {
  if (!value) return "读取中…"
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return "暂无"
  return date.toLocaleString("zh-CN", {
    ...(format === "full" ? { year: "numeric" as const } : {}),
    ...(format !== "time" ? { month: "2-digit" as const, day: "2-digit" as const } : {}),
    hour: "2-digit", minute: "2-digit", hour12: false,
  })
}

/** Compact identity row; refresh/time stay top-right, only page-specific controls get a second row. */
export function PublicPageHeader({ icon: Icon, title, description, controls, metadata, updatedAt, refreshing = false, refreshIn, refreshLabel, onRefresh }: PublicPageHeaderProps) {
  const refreshActionLabel = refreshLabel || `刷新${title}`
  return <header data-public-page-header className="mb-3 min-w-0 shrink-0 overflow-hidden rounded-xl border border-border/70 bg-card text-card-foreground shadow-sm shadow-slate-900/5">
    <div data-public-header-identity className="grid min-w-0 grid-cols-[minmax(0,1fr)_auto] items-start gap-x-2 gap-y-1 px-4 py-3">
      <div data-public-header-heading className="flex min-w-0 items-center gap-2">
        <span data-public-header-icon aria-hidden="true" className="flex size-8 shrink-0 items-center justify-center rounded-lg bg-brand/10 text-brand">
          <Icon className="size-4" />
        </span>
        <h1 className="min-w-0 text-lg font-bold leading-6 text-foreground [overflow-wrap:anywhere]">{title}</h1>
      </div>
      <div data-public-header-status className="flex h-8 shrink-0 items-center justify-end gap-1.5">
        <div data-public-header-metadata className="flex items-center gap-2 text-[11px] leading-4 text-muted-foreground">
          {refreshIn != null ? <span className="hidden lg:inline">{refreshing ? "正在刷新…" : `${refreshIn} 秒后刷新`}</span> : null}
          <span className="whitespace-nowrap" title={`更新于 ${updatedTime(updatedAt, "full")}`}>
            <span className="hidden sm:inline">更新于 </span>
            <time dateTime={updatedAt || undefined}>
              <span className="sm:hidden">{updatedTime(updatedAt, "time")}</span>
              <span className="hidden sm:inline">{updatedTime(updatedAt)}</span>
            </time>
          </span>
        </div>
        {onRefresh ? <Button type="button" variant="outline" size="icon" className="size-8 shrink-0 rounded-md" disabled={refreshing} aria-label={refreshActionLabel} title={refreshActionLabel} onClick={onRefresh}>
          <RefreshCw aria-hidden="true" className={cn("size-3.5", refreshing && "animate-spin motion-reduce:animate-none")} />
        </Button> : null}
      </div>
      <p data-public-header-description className="col-span-2 min-w-0 text-xs leading-4 text-muted-foreground [overflow-wrap:anywhere] sm:col-span-1 sm:col-start-1 sm:pl-10">{description}</p>
      {!controls && metadata ? <div data-public-header-details className="col-span-2 flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-[11px] leading-4 text-muted-foreground">{metadata}</div> : null}
    </div>
    {controls ? <div data-public-header-toolbar className="flex min-w-0 flex-wrap items-center justify-between gap-x-3 gap-y-1.5 border-t border-border/60 bg-muted/15 px-4 py-2">
      <div data-public-header-controls className="flex min-w-0 max-w-full flex-wrap items-center gap-2">{controls}</div>
      {metadata ? <div data-public-header-details className="flex min-w-0 max-w-full flex-wrap items-center gap-x-3 gap-y-1 text-[11px] leading-4 text-muted-foreground [overflow-wrap:anywhere]">{metadata}</div> : null}
    </div> : null}
  </header>
}

export interface PublicPageTab {
  value: string
  label: string
  icon?: ReactNode
  indicator?: ReactNode
}

/** Public header switches share dimensions, active state and keyboard behavior. */
export function PublicPageTabs({ items, value, onValueChange, label }: { items: PublicPageTab[]; value: string; onValueChange: (value: string) => void; label: string }) {
  if (!items.length) return null
  return <div role="tablist" aria-label={label} className="inline-flex max-w-full min-w-0 flex-wrap items-center gap-1 rounded-lg bg-muted/40 p-0.5">
    {items.map((item, index) => <button key={item.value} type="button" role="tab" aria-selected={value === item.value} tabIndex={value === item.value ? 0 : -1} className={cn("flex h-8 shrink-0 items-center justify-center gap-1.5 rounded-md px-3 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring motion-reduce:transition-none", value === item.value ? "bg-card text-foreground shadow-sm" : "text-muted-foreground hover:bg-card/60 hover:text-foreground")} onClick={() => onValueChange(item.value)} onKeyDown={(event) => {
      let next = index
      if (event.key === "ArrowRight") next = (index + 1) % items.length
      else if (event.key === "ArrowLeft") next = (index - 1 + items.length) % items.length
      else if (event.key === "Home") next = 0
      else if (event.key === "End") next = items.length - 1
      else return
      event.preventDefault()
      onValueChange(items[next].value)
      const buttons = event.currentTarget.parentElement?.querySelectorAll<HTMLButtonElement>('button[role="tab"]')
      buttons?.[next]?.focus()
    }}>
      {item.icon ? <span aria-hidden="true" className="inline-flex size-4 shrink-0 items-center justify-center [&>img]:size-4 [&>svg]:size-4">{item.icon}</span> : null}
      {item.label}
      {item.indicator}
    </button>)}
  </div>
}
