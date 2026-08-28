/**
 * API response shapes for GatewayOps backend.
 * Keep in sync with backend/internal/storage/*.go and backend/internal/api/*.go.
 */

export type ChannelType = "newapi" | "sub2api"

export type CredentialMode = "password" | "token"
export type BalanceMode = "auto" | "manual"

export type NotificationChannelType =
  | "telegram"
  | "webhook"
  | "email"
  | "wecom"
  | "dingtalk"
  | "feishu"
  | "bark"

export type CaptchaProviderType =
  | "capsolver"
  | "2captcha"
  | "anticaptcha"
  | "yescaptcha"

export type MonitorJob = "login" | "balance" | "rates"

export type NotificationEvent =
  | "balance_low"
  | "rate_changed"
  | "login_failed"
  | "captcha_failed"
  | "monitor_failed"

export interface Channel {
  id: number
  name: string
  type: ChannelType
  site_url: string
  username: string
  credential_mode: CredentialMode
  balance_mode: BalanceMode
  manual_balance: number
  remark?: string
  turnstile_enabled: boolean
  captcha_config_id?: number | null
  balance_threshold: number
  monitor_enabled: boolean
  last_balance?: number | null
  last_balance_at?: string | null
  last_error?: string
  created_at: string
  updated_at: string
  latest_ratio_changed_at?: string | null
	accounts: ChannelAccount[]
}

export interface ChannelAccount {
  id: number
  is_primary: boolean
  username: string
  credential_mode: CredentialMode
  turnstile_enabled: boolean
  captcha_config_id?: number | null
  last_balance?: number | null
  last_balance_at?: string | null
  last_error?: string
  created_at: string
  updated_at: string
}

export interface ChannelMetric {
  channel_id: number
  consumption_amount: number
  cumulative_recharge_amount: number
  user_charge_amount: number
  matched_account_count: number
  user_charge_complete: boolean
  current_balance: number
}

export interface CaptchaConfig {
  id: number
  name: string
  type: CaptchaProviderType
  endpoint?: string
  extra?: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface RateSnapshot {
  id: number
  channel_id: number
  model_name: string
  description?: string
  ratio: number
  completion_ratio: number
  source?: "upstream" | "manual" | "relay_account" | string
  relay_station_id?: number | null
  relay_account_external_id?: number | null
  first_seen_at: string
  last_seen_at: string
  bound_accounts?: RateBoundAccount[]
}

export interface RateBoundAccount {
  relay_station_id: number
  relay_station_name: string
  relay_account_external_id: number
  relay_account_name: string
}

export interface RateChangeLog {
  id: number
  channel_id: number
  model_name: string
  old_ratio: number | null
  new_ratio: number
  old_completion_ratio?: number | null
  new_completion_ratio?: number
  changed_at: string
}

export interface BalanceSnapshot {
  id: number
  channel_id: number
  balance: number
  sampled_at: string
}

export interface NotificationSubscription {
  channel_id: number
  mode: "all" | "groups"
  groups?: string[]
}

export interface NotificationChannel {
  id: number
  name: string
  type: NotificationChannelType
  enabled: boolean
  subscriptions?: string
  created_at: string
  updated_at: string
}

export interface NotificationLog {
  id: number
  channel_id: number
  event: NotificationEvent
  subject: string
  body: string
  success: boolean
  error_message?: string
  sent_at: string
}

export interface MonitorLog {
  id: number
  channel_id: number
  job: MonitorJob
  success: boolean
  error_message?: string
  duration_ms: number
  started_at: string
  finished_at: string
}

export interface DashboardLowest {
  channel_id: number
  name: string
  balance: number | null
}

export interface DashboardChannelStat {
  id: number
  name: string
  type: string
  site_url: string
  monitor_enabled: boolean
  last_balance?: number | null
  last_error?: string
}

export interface DashboardSummary {
  range: RelayUsageRange
  total_channels: number
  active_channels: number
  failed_channels: number
  total_balance: number
  cumulative_recharge_amount: number
  consumption_amount: number
  user_charge_amount: number
  matched_account_count: number
  user_charge_complete: boolean
  lowest_balance: DashboardLowest | null
  channels: DashboardChannelStat[]
  rate_change_count: number
  recent_rate_changes: RateChangeLog[]
  recent_notification_logs: NotificationLog[]
  relay: RelayDashboardStat[]
  recent_relay_adjustments: RelayAdjustmentView[]
}

export type DashboardRange = "today" | "24h" | "7d" | "30d"
export type RelayUsageRange = DashboardRange | "all"

export interface RelayPricingChange {
  station_id: number
  station_name: string
  channel_external_id: number
  channel_name: string
  old_model_count: number
  new_model_count: number
  old_rule_count: number
  new_rule_count: number
  changed_at: string
}

export interface RelayDashboardStat {
  station_id: number
  station_name: string
  group_count: number
  account_count: number
  assignment_count: number
  mapped_account_count: number
  matched_assignment_count: number
  pricing_account_count: number
  profit_ratio_total: number
  profit_ratio_average: number
  negative_margin_count: number
  risk_account_count: number
  no_profit_account_count: number
  no_safe_candidate_count: number
  unknown_cost_count: number
  protected_account_count: number
  auto_adjust_enabled: boolean
  auto_adjust_no_profit_enabled: boolean
  auto_priority_enabled: boolean
  recent_pricing_changes: RelayPricingChange[]
}

export interface BalanceTrendPoint {
  day: string
  balance: number
}

export interface RelayStation {
  id: number
  name: string
  base_url: string
  api_key_configured: boolean
  auto_adjust_enabled: boolean
  auto_adjust_no_profit_enabled: boolean
  auto_priority_enabled: boolean
  auto_priority_recall_enabled: boolean
  auto_priority_recall_minutes: number
  last_synced_at?: string | null
  last_error?: string
}

export interface RelayGroupOption {
  external_id: number
  name: string
  platform?: string
  status?: string
  is_exclusive?: boolean
  require_oauth_only?: boolean
  sort_order?: number
  account_types?: string[]
  model_types?: string[]
  rate_multiplier: number
}

export type RelayRiskState = "inactive" | "cost_unknown" | "unassigned" | "protected" | "no_profit" | "risk" | "no_safe_candidate"

export interface RelayAccountView {
  external_id: number
  name: string
  base_url?: string
  platform?: string
  type?: string
  status?: string
  schedulable: boolean
  concurrency: number
  current_concurrency: number
  priority: number
  pool_mode: boolean
  pool_mode_retry_count: number
  account_plan?: string
  model_type?: string
  last_used_at?: string | null
  cost_multiplier?: number | null
  cost_source?: "upstream_probe" | "local_group" | "channel_group" | "auto_link" | "" | string
  cost_observed_at?: string | null
  cost_override_mode?: "channel_group" | "manual" | "auto_link" | string
  cost_override_channel_id?: number | null
  cost_override_group?: string
  latency_samples: RelayLatencySample[]
  current_groups: RelayGroupOption[]
  unsafe_groups: RelayGroupOption[]
  no_profit_groups: RelayGroupOption[]
  recommended_group?: RelayGroupOption | null
  downgrade_group?: RelayGroupOption | null
  downgrade_groups: RelayGroupOption[]
  suggested_group_ids: number[]
  current_min_multiplier?: number | null
  margin?: number | null
  risk_state: RelayRiskState
  can_apply: boolean
  usage_total_tokens: number | null
  user_charge_amount: number | null
}

export interface RelayAccountBatchActionResult {
  requested: number
  applied: number
  skipped: number
  failed: number
  errors?: string[]
}

export interface RelayUsageAccountView {
  external_id: number
  usage_total_tokens: number
  user_charge_amount: number
  request_count: number
}

export interface RelayUsageView {
  range: RelayUsageRange
  accounts: RelayUsageAccountView[]
  complete: boolean
  failed_accounts: number
}

export interface RelayRecentUsage {
  id: number
  user_id: number
  user_email: string
  user_name: string
  ip_address: string
  ip_location: string
  group_id: number
  group_name: string
  account_id: number
  account_name: string
  model: string
  request_type: string
  input_tokens: number
  output_tokens: number
  cache_read_tokens: number
  cache_creation_tokens: number
  cache_creation_5m_tokens: number
  cache_creation_1h_tokens: number
  cache_ttl_overridden: boolean
  user_charge: number
  original_cost: number
  first_token_ms: number
  duration_ms: number
  created_at: string
}

export type RelayUserSortKey = "id" | "balance" | "usage" | "current_concurrency" | "last_used_at" | "created_at"

export interface RelayUserManagementItem {
  id: number
  email: string
  username: string
  role: string
  balance: number
  usage: number
  usage_total_tokens: number
  concurrency: number
  current_concurrency: number
  rpm_limit: number
  status: "active" | "disabled"
  last_used_at?: string
  created_at: string
}

export interface RelayUserManagementPage {
  items: RelayUserManagementItem[]
  total: number
  total_balance: number
  page: number
  page_size: number
  pages: number
  range: RelayUsageRange
  complete: boolean
  failed_users: number
}

export interface RelayUserBalanceHistoryItem {
  id: number
  type: string
  value: number
  notes?: string
  code?: string
  used_at?: string
  created_at: string
  validity_days?: number
  group?: { name?: string }
}

export interface RelayUserBalanceHistory {
  user: { id: number; email: string; username?: string; balance?: number; created_at?: string }
  items: RelayUserBalanceHistoryItem[]
  page: number
  pages: number
  total: number
  total_recharged: number
}

export interface RelayAccountTestResult {
  status_code: number
  duration_ms: number
  output: string
}

export interface RelayGroupTestCallResult {
  index: number
  success: boolean
  status_code: number
  duration_ms: number
  output: string
}

export interface RelayGroupTestResult {
  model: string
  requested: number
  succeeded: number
  failed: number
  results: RelayGroupTestCallResult[]
}

export type OperationRange = DashboardRange | "all"

export interface OperationLedgerEntry {
  id: number
  direction: "income" | "expense"
  category: string
  amount: number
  currency: string
  description: string
  source: "manual" | "local_account" | string
  channel_id?: number | null
  channel_name?: string
  relay_station_id?: number | null
  relay_station_name?: string
  local_account_id?: number | null
  occurred_at: string
  created_at: string
  updated_at: string
}

export interface OperationLedgerSummary {
  income_amount: number
  expense_amount: number
  net_amount: number
  account_purchase_amount: number
  upstream_recharge_amount: number
  entry_count: number
  relay_revenue_amount: number
}

export interface OperationLedgerBreakdown {
  direction: "income" | "expense"
  category: string
  amount: number
  count: number
}

export interface LocalAccountSummary {
  total_count: number
  ready_count: number
  deployed_count: number
  disabled_count: number
  unlinked_count: number
  purchase_cost: number
  active_purchase_cost: number
  expected_quota: number
}

export type LocalAccountStatus = "pending" | "ready" | "deployed" | "disabled" | "retired"

export interface LocalAccountView {
  id: number
  name: string
  identifier: string
  platform: string
  account_type: string
  status: LocalAccountStatus
  purchase_cost: number
  expected_quota: number
  purchased_at: string
  expires_at?: string | null
  notes?: string
  relay_station_id?: number | null
  relay_account_external_id?: number | null
  linked_at?: string | null
  relay_station_name?: string
  relay_account_name?: string
  relay_status?: string
  relay_schedulable?: boolean | null
  relay_concurrency?: number | null
  relay_priority?: number | null
  relay_cost?: number | null
  relay_group_names?: string
  relay_snapshot_missing: boolean
  created_at: string
  updated_at: string
}

export interface LocalAccountListResponse {
  accounts: LocalAccountView[]
  summary: LocalAccountSummary
  platforms: string[]
}

export interface OperationSummary {
  range: OperationRange
  ledger: OperationLedgerSummary
  breakdown: OperationLedgerBreakdown[]
  local_pool: LocalAccountSummary
}

export interface OperationLinkAccount {
  external_id: number
  name: string
  platform: string
  account_type: string
  status: string
  schedulable: boolean
  cost?: number | null
}

export interface OperationLinkStation {
  id: number
  name: string
  accounts: OperationLinkAccount[]
}

export interface RelayLatencySample {
  first_token_ms: number
  duration_ms: number
  created_at: string
  model?: string
  request_type?: string
  user_email?: string
}

export interface RelayRiskSummary {
  account_count: number
  risk_account_count: number
  no_profit_account_count: number
  no_safe_candidate_count: number
  unknown_cost_count: number
  auto_adjusted_count: number
  last_adjustment_at?: string | null
}

export interface RelayAdjustmentView {
  id: number
  relay_station_id: number
  relay_station_name?: string
  relay_account_external_id: number
  account_name: string
  account_platform?: string
  cost_multiplier?: number | null
  old_group_ids: number[]
  new_group_ids: number[]
  old_group_names: string[]
  new_group_names: string[]
  recommended_group_id?: number | null
  old_concurrency?: number | null
  new_concurrency?: number | null
  old_priority?: number | null
  new_priority?: number | null
  old_pool_mode_retry_count?: number | null
  new_pool_mode_retry_count?: number | null
  source: "manual" | "auto"
  action: "group_update" | "enable_scheduling" | "disable_scheduling" | string
  success: boolean
  error_message?: string
  applied_at: string
}

export interface RelayChannelView {
  external_id: number
  name: string
  description?: string
  status?: string
  monitor_channel_id?: number | null
  upstream_group?: string
  upstream_ratio?: number | null
  upstream_last_seen_at?: string | null
  ratio_delta?: number | null
  profit_ratio?: number | null
  profit_source?: string
  billing_model_source?: string
  apply_pricing_to_account_stats: boolean
  pricing_model_count: number
  pricing_rule_count: number
  pricing_models: string[]
  pricing_changed_at?: string | null
}

export interface RelayGroupView {
  id: number
  external_id: number
  name: string
  description?: string
  platform?: string
  status?: string
  is_exclusive?: boolean
  require_oauth_only?: boolean
  account_types: string[]
  model_types: string[]
  rate_multiplier: number
	monitor_enabled: boolean
  account_count: number
  priced_account_count: number
  channels: RelayChannelView[]
}

export type PublicGroupMonitorStatus = "available" | "degraded" | "unavailable" | "idle" | "disabled" | "unknown"

export interface PublicGroupMonitorCall {
	success: boolean
	created_at: string
	model?: string
	first_token_ms?: number | null
	duration_ms?: number | null
}

export interface PublicGroupMonitorGroup {
	external_id: number
	name: string
	description?: string
	platform?: string
	rate_multiplier: number
	enabled: boolean
	group_status?: string
	status: PublicGroupMonitorStatus
	data_complete: boolean
	updated_at: string
	latest_call_at?: string | null
	success_count: number
	failure_count: number
	consecutive_failures: number
	failure_summary?: string
	calls: PublicGroupMonitorCall[]
}

export interface PublicGroupMonitorSummary {
	total: number
	available: number
	degraded: number
	unavailable: number
	idle: number
	disabled: number
	unknown: number
}

export interface PublicGroupMonitorView {
	station_id: number
	station_name: string
	updated_at: string
	groups: PublicGroupMonitorGroup[]
	summary: PublicGroupMonitorSummary
}

export interface PublicModelPricingItem {
  model: string
  billing_model?: string
  company: string
  provider?: string
  platform?: string
  billing_mode?: string
  billing_model_source?: string
  price_source: "system_default" | "channel_override" | "channel_override_with_default" | "group_image_pricing" | "unavailable"
  channel_id: number
  channel_name: string
  station_id: number
  station_name: string
  groups?: string[]
  input_price?: number | null
  output_price?: number | null
  cache_write_price?: number | null
  cache_read_price?: number | null
  image_input_price?: number | null
  image_output_price?: number | null
  per_request_price?: number | null
  image_price_1k?: number | null
  image_price_2k?: number | null
  image_price_4k?: number | null
  fast_multiplier?: number | null
  flex_multiplier?: number | null
  intervals?: unknown[]
  time_pricing?: unknown
}

export interface PublicModelPricingView {
  station_id: number
  station_name: string
  updated_at: string
  items: PublicModelPricingItem[]
  summary: {
    models: number
    companies: number
    channels: number
    companies_list: string[]
    platforms: string[]
    billing_modes: string[]
  }
}

export interface RelayMonitorChannelOption {
  id: number
  name: string
  type: string
  groups: string[]
}

export interface RelayOverview {
  station: RelayStation
  groups: RelayGroupView[]
  accounts: RelayAccountView[]
  summary: RelayRiskSummary
  adjustments: RelayAdjustmentView[]
  monitor_channels: RelayMonitorChannelOption[]
  sync_available: boolean
  range: RelayUsageRange
  usage_complete: boolean
  usage_failed_accounts: number
}

export interface SyncSettings {
  channel_configured: boolean
  channel_enabled: boolean
  channel_interval_minutes: number
  relay_rate_configured: boolean
  relay_rate_enabled: boolean
  relay_rate_interval_minutes: number
  relay_snapshot_configured: boolean
  relay_snapshot_enabled: boolean
  relay_snapshot_interval_minutes: number
  relay_snapshot_interval_seconds: number
}
