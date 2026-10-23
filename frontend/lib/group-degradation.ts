export type DegradationMethod = "pelican" | "candy"
export type DegradationEffort = "none" | "low" | "medium" | "high"
export type DegradationStatus = "normal" | "degraded" | "error" | "inconclusive"

export interface GroupDegradationConfig {
  enabled: boolean
  method: "pelican" | "candy" | "both"
  model: string
  judge_model: string
  effort: DegradationEffort
  interval_minutes: number
  pelican_interval_minutes: number
  candy_interval_minutes: number
  history_limit: number
  preview_mode: "hover" | "click"
  result_visibility: "hide" | "show"
}

export interface GroupDegradationResult {
  id: number
  method: DegradationMethod
  model: string
  judge_model: string
  effort: DegradationEffort
  status: DegradationStatus
  reason: string
  html?: string
  answer?: string
  created_at: string
  /** 管理端独有：本次检测实际调用的上游账号与 Sub2API 扣费。公开接口会清空这些字段。 */
  used_account_id?: number
  used_account_name?: string
  input_tokens?: number
  output_tokens?: number
  base_cost?: number
  cost_multiplier?: number
  actual_cost?: number
}

export interface GroupDegradationView {
  config: GroupDegradationConfig
  results: GroupDegradationResult[]
}

export const defaultDegradationConfig: GroupDegradationConfig = {
  enabled: false, method: "pelican", model: "", judge_model: "", effort: "none", interval_minutes: 15, pelican_interval_minutes: 60, candy_interval_minutes: 10, history_limit: 20, preview_mode: "hover", result_visibility: "show",
}

/** Older servers may omit newly added fields. Never retain an unbounded local history. */
export function mergeDegradationConfig(config?: Partial<GroupDegradationConfig> | null): GroupDegradationConfig {
  const merged = { ...defaultDegradationConfig, ...config }
  if (!config?.method) merged.method = "pelican"
  if (!config?.pelican_interval_minutes) merged.pelican_interval_minutes = config?.method === "pelican" ? config.interval_minutes || 60 : 60
  if (!config?.candy_interval_minutes) merged.candy_interval_minutes = config?.method === "candy" ? config.interval_minutes || 10 : 10
  if (!Number.isInteger(merged.history_limit) || merged.history_limit < 1 || merged.history_limit > 1000) merged.history_limit = defaultDegradationConfig.history_limit
  return merged
}

/** Admin-only station settings; no prompt or expected-answer defaults are shipped to public pages. */
export interface DegradationPrompts {
  candy_prompt: string
  candy_expected_answer: string
  pelican_prompt: string
}
export const degradationStatusMeta = {
  normal: { label: "正常", explanation: "通过检测", tone: "text-success", bar: "bg-emerald-500" },
  degraded: { label: "疑似降智", explanation: "未通过检测，疑似降智", tone: "text-warning", bar: "bg-amber-400" },
  error: { label: "检测失败", explanation: "检测失败，本次不判定是否通过", tone: "text-danger", bar: "bg-red-500" },
  inconclusive: { label: "待确认", explanation: "结果待确认，暂无法判定是否通过", tone: "text-muted-foreground", bar: "bg-zinc-400" },
} satisfies Record<DegradationStatus, { label: string; explanation: string; tone: string; bar: string }>

/**
 * Preview-only stage: generated pages are written for a full browser window, so
 * without it the drawing stays pinned to the top-left of the preview frame.
 * Applied to the sandboxed preview copy only; stored/downloaded HTML is untouched.
 */
const previewStageCSS = "html{height:100%;}html,body{margin:0;}body{box-sizing:border-box;min-height:100%;display:flex;flex-direction:column;align-items:center;justify-content:center;}"

/** Restrict model-produced documents to an opaque-origin sandbox with no external resources. */
export function previewHTML(html: string): string {
  const doc = new DOMParser().parseFromString(html, "text/html")
  doc.querySelectorAll("meta[http-equiv],base,iframe,frame,object,embed,form").forEach((element) => element.remove())
  const policy = doc.createElement("meta")
  policy.httpEquiv = "Content-Security-Policy"
  policy.content = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:; connect-src 'none'; media-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"
  doc.head.prepend(policy)
  const stage = doc.createElement("style")
  stage.textContent = previewStageCSS
  doc.head.append(stage)
  return `<!doctype html>\n${doc.documentElement.outerHTML}`
}

/** 管理端降智测试费用统计：分组与账号两条历史合并后分页展示。 */
export interface DegradationUsageItem {
  id: number
  scope: "group" | "account"
  scope_id: number
  scope_name: string
  method: DegradationMethod
  model: string
  judge_model: string
  status: DegradationStatus
  used_account_id: number
  used_account_name: string
  input_tokens: number
  output_tokens: number
  base_cost: number
  cost_multiplier: number
  actual_cost: number
  created_at: string
}

export interface DegradationUsageTotals {
  runs: number
  input_tokens: number
  output_tokens: number
  base_cost: number
  actual_cost: number
}

export interface DegradationUsagePage {
  items: DegradationUsageItem[]
  totals: DegradationUsageTotals
  total: number
  page: number
  page_size: number
  pages: number
}

export type DegradationUsageScope = "all" | "group" | "account"

/** 扣费金额常小于 0.01，保留 6 位并去掉多余的 0，避免小额费用显示成 0。 */
export function formatDegradationCost(value?: number | null) {
  const amount = Number(value ?? 0)
  if (!Number.isFinite(amount)) return "0"
  return amount.toFixed(6).replace(/(\.\d*?)0+$/, "$1").replace(/\.$/, "")
}
