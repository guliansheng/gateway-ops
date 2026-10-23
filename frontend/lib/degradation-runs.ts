"use client"

import { useCallback, useSyncExternalStore } from "react"
import type { GroupDegradationResult } from "@/lib/group-degradation"

/**
 * 手动降智测试的后台运行注册表。
 *
 * 请求不绑定组件生命周期：关闭弹窗、切换页签都不会中断检测，结束后后端照旧
 * 写入历史。重新打开弹窗时订阅到「运行中」状态，完成后自动把结果补进列表。
 */
export type DegradationRunScope = "group" | "account"

export interface DegradationRunState {
  running: boolean
  /** 本次运行的开始与结束时间戳，用于区分不同的运行批次。 */
  startedAt: number
  finishedAt: number
  result: GroupDegradationResult | null
  error: string
}

const idleState: DegradationRunState = { running: false, startedAt: 0, finishedAt: 0, result: null, error: "" }

const states = new Map<string, DegradationRunState>()
const listeners = new Map<string, Set<() => void>>()

export function degradationRunKey(scope: DegradationRunScope, stationID: number, targetID: number) {
  return `${scope}:${stationID}:${targetID}`
}

export function getDegradationRun(key: string): DegradationRunState {
  return states.get(key) ?? idleState
}

function publish(key: string, state: DegradationRunState) {
  states.set(key, state)
  listeners.get(key)?.forEach((listener) => listener())
}

export function subscribeDegradationRun(key: string, listener: () => void) {
  const set = listeners.get(key) ?? new Set<() => void>()
  set.add(listener)
  listeners.set(key, set)
  return () => {
    set.delete(listener)
    if (!set.size) listeners.delete(key)
  }
}

/** 启动一次后台运行。同一目标已有运行任务时直接返回，避免重复计费。 */
export function startDegradationRun(key: string, request: () => Promise<GroupDegradationResult>) {
  const current = states.get(key)
  if (current?.running) return
  const startedAt = Date.now()
  publish(key, { running: true, startedAt, finishedAt: 0, result: null, error: "" })
  void request().then((result) => {
    publish(key, { running: false, startedAt, finishedAt: Date.now(), result, error: "" })
  }).catch((error: unknown) => {
    publish(key, { running: false, startedAt, finishedAt: Date.now(), result: null, error: error instanceof Error ? error.message : "检测执行失败" })
  })
}

export function useDegradationRun(key: string) {
  return useSyncExternalStore(
    useCallback((listener: () => void) => subscribeDegradationRun(key, listener), [key]),
    useCallback(() => getDegradationRun(key), [key]),
    useCallback(() => idleState, []),
  )
}
