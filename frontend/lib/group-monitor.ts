import type { PublicGroupMonitorBucket } from "./api-types"

export const availabilityBands = [
  { minimum: 0.9, label: "≥90%", shape: "h-5 bg-emerald-600", textTone: "text-emerald-700 dark:text-emerald-400" },
  { minimum: 0.7, label: "70%–<90%", shape: "h-3.5 bg-emerald-300", textTone: "text-emerald-600 dark:text-emerald-300" },
  { minimum: 0.5, label: "50%–<70%", shape: "h-2.5 bg-yellow-400", textTone: "text-yellow-700 dark:text-yellow-400" },
  { minimum: 0, label: "<50%", shape: "h-1.5 bg-red-500", textTone: "text-red-700 dark:text-red-400" },
] as const

/** Lower limits are inclusive, so 90%, 70% and 50% each belong to exactly one band. */
export function availabilityBand(value: number | null | undefined) {
  if (value == null || !Number.isFinite(value) || value < 0 || value > 1) return null
  return availabilityBands.find((band) => value >= band.minimum) ?? null
}

export function availabilityBar(bucket: PublicGroupMonitorBucket) {
  if (!bucket.data_complete) return { label: "数据不完整", shape: "h-[3px] bg-zinc-300 dark:bg-zinc-600" }
  if (bucket.total <= 0) return { label: "无请求", shape: "h-[3px] bg-muted-foreground/25" }
  const value = bucket.availability != null && Number.isFinite(bucket.availability)
    ? bucket.availability : bucket.success_count / bucket.total
  const band = availabilityBand(value)
  return band
    ? { label: `可用率 ${band.label}`, shape: band.shape }
    : { label: "可用率未知", shape: "h-[3px] bg-zinc-300 dark:bg-zinc-600" }
}
