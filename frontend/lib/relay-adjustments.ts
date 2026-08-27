import type { RelayAdjustmentView } from "@/lib/api-types"

export type RelayAdjustmentFilter =
  | "all"
  | "group"
  | "scheduling"
  | "priority"

export const relayAdjustmentTabs: { value: RelayAdjustmentFilter; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "group", label: "调组" },
  { value: "scheduling", label: "调度" },
  { value: "priority", label: "优先级" },
]

function adjustmentGroups(names: string[] | undefined, ids: number[]) {
  if (names?.length) return names.join("、")
  if (ids.length) return ids.map((id) => `#${id}`).join("、")
  return "未关联"
}

function valueChange(label: string, oldValue?: number | null, newValue?: number | null) {
  return `${label}：${oldValue ?? "-"} → ${newValue ?? "-"}`
}

export function relayAdjustmentActionLabel(row: RelayAdjustmentView) {
  switch (row.action) {
    case "enable_scheduling":
      return "开启调度"
    case "disable_scheduling":
      return "关闭调度"
    case "priority_update":
      return "调整优先级"
    case "runtime_settings_update":
      return row.old_priority != null && row.new_priority != null ? "调整优先级" : "调整账号参数"
    default:
      return "调整分组"
  }
}

export function relayAdjustmentDetail(row: RelayAdjustmentView) {
  switch (row.action) {
    case "enable_scheduling":
      return "调度状态：关闭 → 开启"
    case "disable_scheduling":
      return "调度状态：开启 → 关闭"
    case "priority_update":
      return valueChange("优先级", row.old_priority, row.new_priority)
    case "runtime_settings_update":
      return valueChange("优先级", row.old_priority, row.new_priority)
    default:
      return `分组：${adjustmentGroups(row.old_group_names, row.old_group_ids)} → ${adjustmentGroups(row.new_group_names, row.new_group_ids)}`
  }
}

export function isRelayGroupAdjustment(row: RelayAdjustmentView) {
  return row.action === "group_update" || ![
    "enable_scheduling",
    "disable_scheduling",
    "priority_update",
    "concurrency_update",
    "runtime_settings_update",
    "retry_count_update",
  ].includes(row.action)
}

export function matchesRelayAdjustmentFilter(row: RelayAdjustmentView, filter: RelayAdjustmentFilter) {
  const scheduling = row.action === "enable_scheduling" || row.action === "disable_scheduling"
  const priority = row.action === "priority_update" || (row.action === "runtime_settings_update" && row.old_priority != null && row.new_priority != null)
  if (filter === "all") return isRelayGroupAdjustment(row) || scheduling || priority
  if (filter === "group") return isRelayGroupAdjustment(row)
  if (filter === "scheduling") return scheduling
  if (filter === "priority") return priority
  return false
}
