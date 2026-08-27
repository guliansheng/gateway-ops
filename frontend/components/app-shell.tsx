"use client"

import { Outlet } from "react-router-dom"
import { MonitorHeader } from "@/components/monitor/monitor-header"
import { DockBar } from "@/components/monitor/dock-bar"

/**
 * AppShell 是所有路由共享的外壳：顶部 header + 中间 Outlet（+ 可选底部 dock）。
 *
 * 当前 Dock 暂时隐藏 —— 单用户 / 少量数据下单页布局比拆页好。
 * 把 SHOW_DOCK 改成 true 即可恢复底部导航 + 路由跳转。
 */
const SHOW_DOCK = false

export function AppShell() {
  return (
    <div className="min-h-screen bg-background">
      <MonitorHeader />
      <main
        className={
          SHOW_DOCK
            ? "w-full space-y-4 px-6 py-4 pb-24"
            : "w-full space-y-4 px-6 py-4"
        }
      >
        <Outlet />
      </main>
      {SHOW_DOCK ? <DockBar /> : null}
      <footer className="px-6 pb-5 pt-2 text-center text-[11px] text-muted-foreground">
        <span>GatewayOps · Copyright (c) 2026 guliansheng and upstream contributors · </span>
        <a className="underline underline-offset-2 hover:text-foreground" href="https://github.com/guliansheng/gateway-ops/blob/main/LICENSE" target="_blank" rel="noopener noreferrer">GNU AGPLv3</a>
        <span> · 无保证</span>
      </footer>
    </div>
  )
}
