import type { ReactNode } from "react"
import { cn } from "@/lib/utils"

export const recentUsageTooltipClassName = "max-h-[calc(var(--spacing)*125)] w-[560px] max-w-[calc(100vw-24px)] overflow-y-auto border border-slate-200 bg-white p-0 text-[11px] text-slate-900 shadow-xl dark:border-slate-200 dark:bg-white dark:text-slate-900 [&>svg]:hidden"
export const recentUsageTooltipNarrowClassName = "max-h-[calc(var(--spacing)*100)] w-[340px] max-w-[calc(100vw-24px)] overflow-y-auto border border-slate-200 bg-white p-0 text-[11px] text-slate-900 shadow-xl dark:border-slate-200 dark:bg-white dark:text-slate-900 [&>svg]:hidden"

export function RecentUsageTooltip({ count, children }: { count: number; children: ReactNode }) {
  return (
    <>
      <div className="sticky top-0 z-10 flex items-center justify-between gap-2 border-b border-slate-200 bg-white px-3 py-2 font-medium shadow-sm">
        <span>最近 {count} 次调用</span>
        <span className="text-slate-500">上半段首字 · 下半段总耗时</span>
      </div>
      <div className="space-y-2 bg-slate-100/60 p-2">{children}</div>
      <div className="sticky bottom-0 flex flex-wrap gap-2 border-t border-slate-200 bg-white px-3 py-2 text-[10px] text-slate-500">
        <span><i className="mr-1 inline-block size-1.5 rounded-full bg-emerald-500" />正常</span>
        <span><i className="mr-1 inline-block size-1.5 rounded-full bg-amber-400" />需关注</span>
        <span><i className="mr-1 inline-block size-1.5 rounded-full bg-orange-500" />较慢</span>
        <span><i className="mr-1 inline-block size-1.5 rounded-full bg-red-500" />严重延迟</span>
      </div>
    </>
  )
}

export function RecentUsageCard({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("rounded-lg bg-white p-2.5 shadow-sm ring-1 ring-slate-200/80", className)}>{children}</div>
}
