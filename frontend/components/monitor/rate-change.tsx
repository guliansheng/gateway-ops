"use client"

import { ratioArrow, relativeTime } from "@/lib/format"
import { cn } from "@/lib/utils"
import type { RateChangeLog, RateSnapshot } from "@/lib/api-types"

export function rateChangeLowered(change?: RateChangeLog | null) {
  return change?.old_ratio != null && change.new_ratio < change.old_ratio
}

export function RateChangeDot({ change, className }: { change?: RateChangeLog | null; className?: string }) {
  if (!change || change.old_ratio == null) return null
  const lowered = rateChangeLowered(change)
  return (
    <span
      aria-label={lowered ? "倍率最近下降" : "倍率最近上升"}
      className={cn(
        "size-2 shrink-0 rounded-full ring-2 ring-background",
        lowered ? "bg-success" : "bg-danger",
        className,
      )}
    />
  )
}

export function RateTooltipBody({ rate }: { rate: RateSnapshot }) {
  const change = rate.latest_ratio_change
  const lowered = rateChangeLowered(change)
  return (
    <>
      <p className="font-medium">{rate.model_name}</p>
      {rate.description ? (
        <p className="mt-0.5 text-background/80">{rate.description}</p>
      ) : (
        <p className="mt-0.5 italic text-background/75">{"(无描述)"}</p>
      )}
      <p className="mt-0.5 text-background/80">
        {"最近更新："}
        {change ? relativeTime(change.changed_at) : "未更新"}
      </p>
      {change ? (
        <p className={cn("mt-0.5", lowered ? "text-success" : "text-danger")}>
          {"最近倍率变动："}
          {ratioArrow(change.old_ratio, change.new_ratio)}
        </p>
      ) : null}
    </>
  )
}
