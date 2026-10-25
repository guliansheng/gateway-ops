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
  /** 管理端独有：本行来自账号降智测试导入时记录来源账号；公开接口会清空。 */
  source_account_id?: number
  source_account_name?: string
  /** 手动改判时保存被覆盖的原判定；公开接口会清空。 */
  original_status?: DegradationStatus
  adjusted_at?: string
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
  /** 鹈鹕测试的评审参考标准，仅用于评审模型，不发送给作答模型。 */
  pelican_reference: string
}
export const degradationStatusMeta = {
  normal: { label: "正常", explanation: "通过检测", tone: "text-success", bar: "bg-emerald-500" },
  degraded: { label: "疑似降智", explanation: "未通过检测，疑似降智", tone: "text-warning", bar: "bg-amber-400" },
  error: { label: "检测失败", explanation: "检测失败，本次不判定是否通过", tone: "text-danger", bar: "bg-red-500" },
  inconclusive: { label: "待确认", explanation: "结果待确认，暂无法判定是否通过", tone: "text-muted-foreground", bar: "bg-zinc-400" },
} satisfies Record<DegradationStatus, { label: string; explanation: string; tone: string; bar: string }>

/** 已存下生成内容：评审超时/失败时可以只重评审，不必再花一次生成费用。 */
export function degradationHasEvidence(result?: Partial<GroupDegradationResult> | null) {
  return Boolean(result?.html?.trim() || result?.answer?.trim())
}

/** 失败原因只报类别：管理员要判断的是“重跑还是改判”，不需要上游原文。 */
export type DegradationFailureKind = "gateway" | "request" | "judge"

export const degradationFailureMeta = {
  gateway: { label: "网关问题" },
  request: { label: "请求问题" },
  judge: { label: "评审问题" },
} satisfies Record<DegradationFailureKind, { label: string }>

// 上游原文里出现这些词就说明请求本身不可用，而不是网关抖动。
const requestFailurePattern = /密钥|凭据|凭证|令牌|账号|权限|协议|参数|格式|不支持|未配置|无效|不存在|api ?key|token|credential|permission|protocol|parameter|invalid|unsupported|unauthorized|forbidden|\b(400|401|403|404|422)\b/i

/**
 * 把一段上游错误归到一个类别。已有生成内容却判失败的属于评审环节，没有内容
 * 则先看理由里的请求类特征词，剩下的都按网关问题处理（可重跑）。
 */
export function degradationFailureKind(result?: Partial<GroupDegradationResult> | null): DegradationFailureKind | "" {
  if (!result || result.status !== "error") return ""
  if (degradationHasEvidence(result)) return "judge"
  const reason = (result.reason ?? "").trim()
  if (/评审|判定/.test(reason)) return "judge"
  return requestFailurePattern.test(reason) ? "request" : "gateway"
}

/** 列表外层只放类别标签，完整理由留在 title 提示里。 */
export function degradationFailureSummary(result?: Partial<GroupDegradationResult> | null) {
  const kind = degradationFailureKind(result)
  return kind ? degradationFailureMeta[kind].label : ""
}

/** 历史筛选：按测试方式或按判定结果（成功 = 非 error）。 */
export type DegradationHistoryFilter = "all" | DegradationMethod | "success" | "error"

export const degradationHistoryFilterLabels = { all: "全部", success: "成功", error: "错误", pelican: "鹈鹕", candy: "糖果" } satisfies Record<DegradationHistoryFilter, string>

export function filterDegradationHistory(results: GroupDegradationResult[], filter: DegradationHistoryFilter) {
  switch (filter) {
    case "success": return results.filter((result) => result.status !== "error")
    case "error": return results.filter((result) => result.status === "error")
    case "pelican": return results.filter((result) => result.method !== "candy")
    case "candy": return results.filter((result) => result.method === "candy")
    default: return results
  }
}

/**
 * 与后端 prune 同口径：成功与失败各留上限，失败记录不占“最多保留条数”的名额。
 * 服务端返回的顺序已经是 created_at DESC，所以这里保持原顺序、按桶计数截断，
 * 既能保留“最新一条在最前”，也能让刚失败的一条不会被本地 slice 立刻隐藏。
 */
export function limitDegradationHistory(results: GroupDegradationResult[], limit: number) {
  const cap = Number.isInteger(limit) && limit >= 1 ? limit : defaultDegradationConfig.history_limit
  let succeeded = 0
  let failed = 0
  return results.filter((result) => (result.status === "error" ? ++failed <= cap : ++succeeded <= cap))
}
/**
 * Preview-only stage: generated pages are written for a full browser window, so
 * without it the drawing stays pinned to the top-left of the preview frame.
 * Applied to the sandboxed preview copy only; stored/downloaded HTML is untouched.
 */
const previewStageCSS = "html{height:100%;}html,body{margin:0;}body{box-sizing:border-box;min-height:100%;display:flex;flex-direction:column;align-items:center;justify-content:center;}"

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
