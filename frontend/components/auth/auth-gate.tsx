"use client"

import { useAuth } from "@/lib/auth-context"
import { LoginPage } from "@/components/auth/login-page"
import type { ReactNode } from "react"

/**
 * AuthGate 把根渲染分成三态：
 *   loading       本地有 token 但还没验完 — 显示占位
 *   anonymous     未登录 — 显示登录页
 *   authenticated 已登录 — 显示业务内容
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const { status } = useAuth()

  if (status === "loading") {
    return (
      <div className="flex min-h-screen items-center justify-center text-sm text-muted-foreground">
        加载中…
      </div>
    )
  }
  if (status === "anonymous") {
    return <LoginPage />
  }
  return <>{children}</>
}
