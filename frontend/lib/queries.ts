"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { apiFetch } from "@/lib/api"
import { useRefreshTick } from "@/lib/refresh-context"
import type {
  BalanceTrendPoint,
  CaptchaConfig,
  Channel,
  ChannelMetric,
  DashboardSummary,
  NotificationChannel,
  NotificationLog,
  RateChangeLog,
  RateSnapshot,
  RelayOverview,
  RelayUsageView,
  RelayRecentUsage,
  RelayUserManagementPage,
  RelayUserSortKey,
  RelayUserBalanceHistory,
  RelayStation,
  DashboardRange,
  RelayUsageRange,
  SyncSettings,
  LocalAccountListResponse,
  OperationRange,
  OperationLedgerEntry,
  OperationLinkStation,
  OperationSummary,
} from "@/lib/api-types"

export interface QueryState<T> {
  data: T | null
  loading: boolean
  refreshing: boolean
  error: string | null
  refetch: () => Promise<void>
}

/**
 * In-flight 请求去重：同一个 URL 在同一个 tick 内只发一次，所有 useApi 共享 Promise。
 *
 * 为什么需要：useDashboardSummary() 在 5 个组件里都被调用，没去重的话每次 mount /
 * refresh 都会发 5 个相同请求。开发环境叠加 StrictMode 翻倍后会更夸张。
 */
const inflight = new Map<string, Promise<unknown>>()

/** Cache 已完成的响应一小段时间，便于同一帧内挂载的多个组件共享结果（即使第一次的 Promise 已经 resolve）。 */
interface CacheEntry {
  data: unknown
  expiresAt: number
}
const cache = new Map<string, CacheEntry>()
const CACHE_TTL_MS = 800

interface ApiState<T> {
  path: string | null
  data: T | null
  loading: boolean
  refreshing: boolean
  error: string | null
}

function cacheKey(path: string, tick: number, bump: number) {
  return `${path}#${tick}#${bump}`
}

function fetchShared<T>(path: string, key: string): Promise<T> {
  const now = Date.now()

  const cached = cache.get(key)
  if (cached && cached.expiresAt > now) {
    return Promise.resolve(cached.data as T)
  }

  const existing = inflight.get(key) as Promise<T> | undefined
  if (existing) return existing

  const p = apiFetch<T>(path)
    .then((d) => {
      cache.set(key, { data: d, expiresAt: Date.now() + CACHE_TTL_MS })
      return d
    })
    .finally(() => {
      // 让下一帧（refresh tick++）拉到新的数据，不要永远 hold 住旧 promise
      inflight.delete(key)
    })
  inflight.set(key, p)
  return p
}

/**
 * useApi 通用数据获取 hook（stale-while-revalidate）。
 * - 首次加载：loading = true，组件显示加载占位
 * - 后续刷新（refresh tick / refetch）：保留旧 data，refreshing=true 供表格显示刷新反馈
 * - 请求路径变化：清空旧 data，避免把上一条查询的结果展示成新查询
 * - 同 URL + 同 tick 的并发调用共享一次请求
 */
function useApi<T>(path: string | null): QueryState<T> {
  const [state, setState] = useState<ApiState<T>>({
    path,
    data: null,
    loading: path !== null,
    refreshing: path !== null,
    error: null,
  })
  const [bump, setBump] = useState(0)
  const bumpRef = useRef(0)
  const globalTick = useRefreshTick()
  const refetchWaiters = useRef<Array<{ path: string; bump: number; resolve: () => void; reject: (error: unknown) => void }>>([])

  useEffect(() => () => {
    const waiters = refetchWaiters.current.splice(0)
    waiters.forEach(({ resolve }) => resolve())
  }, [])

  useEffect(() => {
    const obsoleteWaiters = refetchWaiters.current.filter((waiter) => waiter.path !== path)
    refetchWaiters.current = refetchWaiters.current.filter((waiter) => waiter.path === path)
    obsoleteWaiters.forEach(({ resolve }) => resolve())
    if (path === null) {
      setState({ path: null, data: null, loading: false, refreshing: false, error: null })
      return
    }
    const requestBump = bump
    let cancelled = false
    setState((previous) => {
      if (previous.path !== path) {
        return { path, data: null, loading: true, refreshing: true, error: null }
      }
      if (previous.data === null) {
        return { ...previous, loading: true, refreshing: true, error: null }
      }
      return { ...previous, loading: false, refreshing: true, error: null }
    })
    fetchShared<T>(path, cacheKey(path, globalTick, bump))
      .then((d) => {
        if (cancelled) return
        setState({ path, data: d, loading: false, refreshing: false, error: null })
        const waiters = refetchWaiters.current.filter((waiter) => waiter.path === path && waiter.bump <= requestBump)
        refetchWaiters.current = refetchWaiters.current.filter((waiter) => waiter.path !== path || waiter.bump > requestBump)
        waiters.forEach(({ resolve }) => resolve())
      })
      .catch((e: Error) => {
        if (cancelled) return
        setState((previous) => (
          previous.path === path
            ? { ...previous, loading: false, refreshing: false, error: e.message }
            : previous
        ))
        const waiters = refetchWaiters.current.filter((waiter) => waiter.path === path && waiter.bump <= requestBump)
        refetchWaiters.current = refetchWaiters.current.filter((waiter) => waiter.path !== path || waiter.bump > requestBump)
        waiters.forEach(({ reject }) => reject(e))
      })
    return () => {
      cancelled = true
    }
  }, [path, bump, globalTick])

  const current = state.path === path
    ? state
    : { path, data: null, loading: path !== null, refreshing: path !== null, error: null }

  return {
    data: current.data,
    loading: current.loading,
    refreshing: current.refreshing,
    error: current.error,
    refetch: useCallback(() => {
      if (path === null) return Promise.resolve()
      const nextBump = bumpRef.current + 1
      bumpRef.current = nextBump
      return new Promise<void>((resolve, reject) => {
        refetchWaiters.current.push({ path, bump: nextBump, resolve, reject })
        setBump(nextBump)
      })
    }, [path]),
  }
}

export function useDashboardSummary(range: RelayUsageRange = "today") {
  return useApi<DashboardSummary>(`/dashboard/summary?range=${range}`)
}

export type BalanceTrendRange = DashboardRange

export function useBalanceTrend(range: BalanceTrendRange = "7d") {
  return useApi<BalanceTrendPoint[]>(`/dashboard/balance-trend?range=${range}`)
}

export function useChannels() {
  return useApi<Channel[]>("/channels")
}

export function useChannelMetrics(range: RelayUsageRange = "today") {
  return useApi<ChannelMetric[]>(`/channels/metrics?range=${range}`)
}

export function useChannelRates(channelID: number | null) {
  return useApi<RateSnapshot[]>(channelID == null ? null : `/channels/${channelID}/rates`)
}

export function useRateChanges(limit = 20, channelID?: number, ratioOnly = false) {
  const q = new URLSearchParams()
  q.set("limit", String(limit))
  if (channelID != null) q.set("channel_id", String(channelID))
  if (ratioOnly) q.set("ratio_only", "true")
  return useApi<RateChangeLog[]>(`/rate-changes?${q.toString()}`)
}

export function useLatestRatioChanges(channelID: number | null) {
  const path = channelID == null
    ? null
    : `/rate-changes?channel_id=${channelID}&latest_per_group=true`
  return useApi<RateChangeLog[]>(path)
}

export function useNotificationChannels() {
  return useApi<NotificationChannel[]>("/notifications/channels")
}

export function useNotificationLogs(limit = 20) {
  return useApi<NotificationLog[]>(`/notifications/logs?limit=${limit}`)
}

export function useCaptchaConfigs() {
  return useApi<CaptchaConfig[]>("/captcha-configs")
}

export function useRelayStations() {
  return useApi<RelayStation[]>("/relay-stations")
}

export function useRelayOverview(stationID: number | null) {
  return useApi<RelayOverview>(stationID == null ? null : `/relay-stations/${stationID}`)
}

export function useRelayUsage(stationID: number | null, range: RelayUsageRange = "today") {
  return useApi<RelayUsageView>(stationID == null ? null : `/relay-stations/${stationID}/usage?range=${range}`)
}

export function useRelayRecentUsage(stationID: number | null) {
  return useApi<RelayRecentUsage[]>(stationID == null ? null : `/relay-stations/${stationID}/usage/recent?limit=100`)
}

export function useRelayUsers(stationID: number | null, options: {
  page: number
  pageSize: number
  search: string
  range: RelayUsageRange
  sortBy: RelayUserSortKey
  sortOrder: "asc" | "desc"
}) {
  if (stationID == null) return useApi<RelayUserManagementPage>(null)
  const query = new URLSearchParams({
    page: String(options.page),
    page_size: String(options.pageSize),
    range: options.range,
    sort_by: options.sortBy,
    sort_order: options.sortOrder,
  })
  if (options.search.trim()) query.set("search", options.search.trim())
  return useApi<RelayUserManagementPage>(`/relay-stations/${stationID}/users?${query.toString()}`)
}

export function useRelayUserBalanceHistory(stationID: number | null, userID: number | null, page = 1, type = "") {
  const query = type ? `&type=${encodeURIComponent(type)}` : ""
  return useApi<RelayUserBalanceHistory>(stationID == null || userID == null ? null : `/relay-stations/${stationID}/users/${userID}/balance-history?page=${page}&page_size=15${query}`)
}

export function useSyncSettings() {
  return useApi<SyncSettings>("/sync-settings")
}

export function useOperationSummary(range: OperationRange = "today") {
  return useApi<OperationSummary>(`/operations/summary?range=${range}`)
}

export function useOperationLedger(range: OperationRange = "30d", filters: { direction?: string; category?: string } = {}) {
  const query = new URLSearchParams({ range })
  if (filters.direction && filters.direction !== "all") query.set("direction", filters.direction)
  if (filters.category && filters.category !== "all") query.set("category", filters.category)
  return useApi<OperationLedgerEntry[]>(`/operations/ledger?${query.toString()}`)
}

export function useLocalAccounts(filters: { status?: string; platform?: string; q?: string } = {}) {
  const query = new URLSearchParams()
  if (filters.status) query.set("status", filters.status)
  if (filters.platform) query.set("platform", filters.platform)
  if (filters.q) query.set("q", filters.q)
  const suffix = query.toString() ? `?${query.toString()}` : ""
  return useApi<LocalAccountListResponse>(`/operations/local-accounts${suffix}`)
}

export function useOperationLinkOptions() {
  return useApi<OperationLinkStation[]>("/operations/link-options")
}
