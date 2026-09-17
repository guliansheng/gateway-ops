"use client"

import { useEffect, useState, type FormEvent } from "react"
import { HelpCircle, Plus, Trash2 } from "lucide-react"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Button } from "@/components/ui/button"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Switch } from "@/components/ui/switch"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import type { BalanceMode, CaptchaConfig, Channel, ChannelType, CredentialMode, RequestKV } from "@/lib/api-types"
import { apiFetch } from "@/lib/api"
import { useTriggerRefresh } from "@/lib/refresh-context"
import { useCaptchaConfigs } from "@/lib/queries"
import { cn } from "@/lib/utils"

interface ChannelFormDialogProps {
  open: boolean
  onOpenChange: (v: boolean) => void
  /** 编辑模式时传入；为空表示新增 */
  channel?: Channel | null
}

/**
 * FormState 是表单的所有可编辑字段。
 *
 * password 字段在 token 模式下不展示；
 * token 字段（cookie / user_id / access_token）在 password 模式下不展示。
 * 这些状态对应保留在内存里，方便用户来回切换不丢失输入。
 */
interface FormState {
  name: string
  type: ChannelType
  site_url: string
  username: string
  password: string

  credential_mode: CredentialMode
  balance_mode: BalanceMode
  manual_balance: string
  remark: string
  login_headers: RequestKV[]
  login_params: RequestKV[]
  // NewAPI token 模式
  newapi_auth_type: "cookie" | "access_token"
  newapi_cookie: string
  newapi_user_id: string
  newapi_access_token: string
  newapi_token_headers: RequestKV[]
  // Sub2API token 模式
  sub2api_access_token: string
  sub2api_refresh_token: string

  balance_threshold: string
  monitor_enabled: boolean
  turnstile_enabled: boolean
  captcha_config_id: string // "" 表示不绑定
  additional_accounts: AdditionalAccountForm[]
}

interface AdditionalAccountForm {
  id?: number
  username: string
  password: string
  credential_mode: CredentialMode
  initial_credential_mode?: CredentialMode
  newapi_auth_type: "cookie" | "access_token"
  newapi_cookie: string
  newapi_user_id: string
  newapi_access_token: string
  newapi_token_headers: RequestKV[]
  sub2api_access_token: string
  sub2api_refresh_token: string
  turnstile_enabled: boolean
  captcha_config_id: string
}

function defaultLoginConfig(type: ChannelType): { headers: RequestKV[]; params: RequestKV[] } {
  return {
    headers: [{ key: "Content-Type", value: "application/json" }],
    params: type === "newapi"
      ? [{ key: "username", value: "{{username}}" }, { key: "password", value: "{{password}}" }]
      : [
          { key: "email", value: "{{username}}" },
          { key: "password", value: "{{password}}" },
          { key: "turnstile_token", value: "{{turnstile_token}}" },
        ],
  }
}

function defaultNewAPITokenHeaders(): RequestKV[] {
  return [{ key: "Authorization", value: "Bearer {{token}}" }]
}

function emptyAdditionalAccount(mode: CredentialMode = "password"): AdditionalAccountForm {
  return {
    username: "",
    password: "",
    credential_mode: mode,
    newapi_auth_type: "cookie",
    newapi_cookie: "",
    newapi_user_id: "",
    newapi_access_token: "",
    newapi_token_headers: defaultNewAPITokenHeaders(),
    sub2api_access_token: "",
    sub2api_refresh_token: "",
    turnstile_enabled: false,
    captcha_config_id: "",
  }
}

function editableManualBalance(value: number | null | undefined): string {
  if (value == null || !Number.isFinite(value)) return "0"
  // Floating-point settlements can leave an unhelpful tail such as
  // 26.92056263999999. Keep enough practical precision without exposing it.
  return value.toFixed(10).replace(/\.?0+$/, "")
}

function initialState(c?: Channel | null): FormState {
  const type = c?.type ?? "newapi"
  const defaults = defaultLoginConfig(type)
  return {
    name: c?.name ?? "",
    type,
    site_url: c?.site_url ?? "",
    username: c?.username ?? "",
    password: "",
    credential_mode: c?.credential_mode ?? "password",
    balance_mode: c?.balance_mode ?? "auto",
    manual_balance: c?.balance_mode === "manual" ? editableManualBalance(c.last_balance ?? c.manual_balance) : (c?.manual_balance != null ? String(c.manual_balance) : "0"),
    remark: c?.remark ?? "",
    login_headers: c?.login_headers?.map((item) => ({ ...item })) ?? defaults.headers,
    login_params: c?.login_params?.map((item) => ({ ...item })) ?? defaults.params,
    newapi_auth_type: c?.newapi_auth_type ?? "cookie",
    newapi_cookie: "",
    newapi_user_id: "",
    newapi_access_token: "",
    newapi_token_headers: c?.newapi_token_headers?.map((item) => ({ ...item })) ?? defaultNewAPITokenHeaders(),
    sub2api_access_token: "",
    sub2api_refresh_token: "",
    balance_threshold: c?.balance_threshold != null ? String(c.balance_threshold) : "0",
    monitor_enabled: c?.monitor_enabled ?? true,
    turnstile_enabled: c?.turnstile_enabled ?? false,
    captcha_config_id: c?.captcha_config_id != null ? String(c.captcha_config_id) : "",
    additional_accounts: (c?.accounts ?? [])
      .filter((account) => !account.is_primary)
      .map((account) => ({
        id: account.id,
        username: account.username,
        password: "",
        credential_mode: account.credential_mode,
        initial_credential_mode: account.credential_mode,
        newapi_auth_type: account.newapi_auth_type ?? "cookie",
        newapi_cookie: "",
        newapi_user_id: "",
        newapi_access_token: "",
        newapi_token_headers: account.newapi_token_headers?.map((item) => ({ ...item })) ?? defaultNewAPITokenHeaders(),
        sub2api_access_token: "",
        sub2api_refresh_token: "",
        turnstile_enabled: account.turnstile_enabled,
        captcha_config_id: account.captcha_config_id != null ? String(account.captcha_config_id) : "",
      })),
  }
}

/**
 * buildTokenCredential 把当前表单里的 token 字段序列化成后端期望的 JSON 字符串。
 * 字段命名与 channel/service.go 里的 NewAPITokenCredential / Sub2APITokenCredential 对齐。
 */
function buildTokenCredential(form: FormState): string {
  if (form.type === "newapi") {
    if (form.newapi_auth_type === "access_token") {
      return JSON.stringify({
        auth_type: "access_token",
        token: form.newapi_access_token.trim(),
        headers: form.newapi_token_headers,
      })
    }
    return JSON.stringify({
      auth_type: "cookie",
      cookie: form.newapi_cookie.trim(),
      user_id: form.newapi_user_id.trim(),
    })
  }
  return JSON.stringify({
    access_token: form.sub2api_access_token.trim(),
    refresh_token: form.sub2api_refresh_token.trim(),
  })
}

function buildAdditionalTokenCredential(account: AdditionalAccountForm, type: ChannelType): string {
  if (type === "newapi") {
    if (account.newapi_auth_type === "access_token") {
      return JSON.stringify({ auth_type: "access_token", token: account.newapi_access_token.trim(), headers: account.newapi_token_headers })
    }
    return JSON.stringify({ auth_type: "cookie", cookie: account.newapi_cookie.trim(), user_id: account.newapi_user_id.trim() })
  }
  return JSON.stringify({ access_token: account.sub2api_access_token.trim(), refresh_token: account.sub2api_refresh_token.trim() })
}

function buildAdditionalAccountPayloads(accounts: AdditionalAccountForm[], type: ChannelType) {
  return accounts.map((account, index) => {
    const label = `附加账号 ${index + 1}`
    const isExisting = account.id != null
    const modeChanged = isExisting && account.credential_mode !== account.initial_credential_mode
    const isToken = account.credential_mode === "token"
    const payload: Record<string, unknown> = {
      ...(account.id != null ? { id: account.id } : {}),
      username: account.username.trim(),
      credential_mode: account.credential_mode,
      turnstile_enabled: !isToken && account.turnstile_enabled,
      captcha_config_id: null,
    }
    if (!account.username.trim()) throw new Error(`${label}必须填写账号或备注`)

    if (isToken) {
      const hasCredential = type === "newapi"
        ? account.newapi_auth_type === "access_token"
          ? Boolean(account.newapi_access_token.trim())
          : Boolean(account.newapi_cookie.trim() || account.newapi_user_id.trim())
        : Boolean(account.sub2api_access_token.trim() || account.sub2api_refresh_token.trim())
      if (!isExisting || modeChanged || hasCredential) {
        if (type === "newapi" && account.newapi_auth_type === "cookie" && (!account.newapi_cookie.trim() || !account.newapi_user_id.trim())) {
          throw new Error(`${label}的 NewAPI Cookie 和 User ID 必须同时填写`)
        }
        if (type === "newapi" && account.newapi_auth_type === "access_token" && !account.newapi_access_token.trim()) {
          throw new Error(`${label}必须填写 NewAPI 访问令牌`)
        }
        if (type === "sub2api" && (!account.sub2api_access_token.trim() || !account.sub2api_refresh_token.trim())) {
          throw new Error(`${label}必须同时填写 Sub2API Access Token 和 Refresh Token`)
        }
        payload.token_credential = buildAdditionalTokenCredential(account, type)
      }
      return payload
    }

    if (!isExisting || modeChanged) {
      if (!account.password) throw new Error(`${label}必须填写密码`)
    }
    if (account.password) payload.password = account.password
    if (account.turnstile_enabled) {
      if (!account.captcha_config_id) throw new Error(`${label}启用 Turnstile 时必须选择打码 provider`)
      payload.captcha_config_id = Number(account.captcha_config_id)
    }
    return payload
  })
}

export function ChannelFormDialog({ open, onOpenChange, channel }: ChannelFormDialogProps) {
  const [form, setForm] = useState<FormState>(() => initialState(channel))
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const refresh = useTriggerRefresh()
  const captchas = useCaptchaConfigs()

  // 打开 / 切换目标渠道时重置表单。
  useEffect(() => {
    if (open) {
      setForm(initialState(channel))
      setError(null)
    }
  }, [open, channel])

  const isEdit = !!channel
  const isManualBalance = form.balance_mode === "manual"
  const isTokenMode = form.credential_mode === "token"
  // 编辑模式下，若 credential_mode 没变，token / password 都可以留空表示不修改。
  const modeChanged = isEdit && (
    form.credential_mode !== (channel?.credential_mode ?? "password") ||
    form.balance_mode !== (channel?.balance_mode ?? "auto")
  )

  async function handleSubmit(e: FormEvent<HTMLFormElement>) {
    e.preventDefault()
    setError(null)
    setSubmitting(true)
    try {
      const threshold = Number(form.balance_threshold)
      if (!Number.isFinite(threshold) || threshold < 0) {
        throw new Error("余额阈值必须是非负数")
      }

      const manualBalance = Number(form.manual_balance)
      if (!Number.isFinite(manualBalance) || manualBalance < 0) {
        throw new Error("手动当前余额必须是非负数字")
      }

      if (isManualBalance) {
        const body: Record<string, unknown> = {
          name: form.name,
          site_url: form.site_url,
          balance_mode: "manual",
          manual_balance: manualBalance,
          remark: form.remark.trim(),
          balance_threshold: threshold,
          monitor_enabled: form.monitor_enabled,
        }
        if (isEdit) {
          await apiFetch(`/channels/${channel!.id}`, { method: "PUT", body: JSON.stringify(body) })
        } else {
          await apiFetch("/channels", {
            method: "POST",
            body: JSON.stringify({ ...body, type: form.type }),
          })
        }
        onOpenChange(false)
        refresh()
        return
      }

      // token 模式：用户填的字段对应不同 connector 的 token JSON
      let tokenCredential = ""
      if (isTokenMode) {
        if (form.type === "newapi") {
          if (form.newapi_auth_type === "access_token") {
            if (!isEdit || modeChanged || form.newapi_access_token) {
              if (!form.newapi_access_token.trim()) throw new Error("NewAPI 访问令牌模式必须填写 Token")
            }
          } else if (!isEdit || modeChanged || form.newapi_cookie || form.newapi_user_id) {
            if (!form.newapi_cookie.trim()) throw new Error("NewAPI Cookie 模式必须填写 Cookie")
            if (!form.newapi_user_id.trim()) throw new Error("NewAPI Cookie 模式必须填写 User ID")
          }
        } else if (!isEdit || modeChanged || form.sub2api_access_token || form.sub2api_refresh_token) {
          if (!form.sub2api_access_token.trim() || !form.sub2api_refresh_token.trim()) {
            throw new Error("Sub2API token 模式必须同时填写 Access Token 和 Refresh Token")
          }
        }
        // 只在用户填写了字段、或者首次创建、或者切换模式时下发 token_credential
        if (
          !isEdit ||
          modeChanged ||
          form.newapi_cookie ||
          form.newapi_user_id ||
          form.newapi_access_token ||
          (form.type === "newapi" && form.newapi_auth_type === "access_token") ||
          form.sub2api_access_token ||
          form.sub2api_refresh_token
        ) {
          tokenCredential = buildTokenCredential(form)
        }
      }

      // 打码 provider 只在 password 模式 + 启用 Turnstile 时生效
      const useCaptcha = !isTokenMode && form.turnstile_enabled
      const captchaConfigID =
        useCaptcha && form.captcha_config_id ? Number(form.captcha_config_id) : null
      if (useCaptcha && captchaConfigID == null) {
        throw new Error("启用 Turnstile 时必须选择一个打码 provider")
      }

      // password 模式下的密码校验
      if (!isTokenMode) {
        if (!isEdit && !form.password) throw new Error("新建时必须填写密码")
        if (modeChanged && !form.password) throw new Error("切换到账号密码模式时必须填写密码")
      }

      const additionalAccounts = buildAdditionalAccountPayloads(form.additional_accounts, form.type)

      if (isEdit) {
        const body: Record<string, unknown> = {
          name: form.name,
          site_url: form.site_url,
          username: form.username,
          credential_mode: form.credential_mode,
          login_headers: form.login_headers,
          login_params: form.login_params,
          balance_mode: "auto",
          remark: form.remark.trim(),
          balance_threshold: threshold,
          monitor_enabled: form.monitor_enabled,
          turnstile_enabled: !isTokenMode && form.turnstile_enabled,
          captcha_config_id: captchaConfigID,
		  accounts: additionalAccounts,
        }
        if (!isTokenMode && form.password) body.password = form.password
        if (isTokenMode && tokenCredential) body.token_credential = tokenCredential
        await apiFetch(`/channels/${channel!.id}`, {
          method: "PUT",
          body: JSON.stringify(body),
        })
      } else {
        await apiFetch(`/channels`, {
          method: "POST",
          body: JSON.stringify({
            name: form.name,
            type: form.type,
            site_url: form.site_url,
            username: form.username,
            credential_mode: form.credential_mode,
            login_headers: form.login_headers,
            login_params: form.login_params,
            balance_mode: "auto",
            manual_balance: 0,
            remark: form.remark.trim(),
            password: isTokenMode ? "" : form.password,
            token_credential: isTokenMode ? tokenCredential : "",
            balance_threshold: threshold,
            monitor_enabled: form.monitor_enabled,
            turnstile_enabled: !isTokenMode && form.turnstile_enabled,
            captcha_config_id: captchaConfigID,
			accounts: additionalAccounts,
          }),
        })
      }
      onOpenChange(false)
      refresh()
    } catch (e) {
      const err = e as Error
      setError(err.message || "保存失败")
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[90vh] min-w-0 overflow-x-hidden overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{isEdit ? "编辑渠道" : "新增渠道"}</DialogTitle>
          <DialogDescription>
            {isManualBalance ? "手动余额不需要上游用户名密码；已绑定账号产生消费后会自动扣减。" : "一个渠道可维护多个上游账号，余额会自动汇总。"}
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={handleSubmit} className="min-w-0 space-y-3" autoComplete="off">
          <div className="space-y-1.5">
            <Label htmlFor="name">渠道名</Label>
            <Input
              id="name"
              value={form.name}
              onChange={(e) => setForm({ ...form, name: e.target.value })}
              required
              disabled={submitting}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="type">类型</Label>
            <Select
              value={form.type}
              onValueChange={(v) => {
                const type = v as ChannelType
                const defaults = defaultLoginConfig(type)
                setForm({ ...form, type, login_headers: defaults.headers, login_params: defaults.params })
              }}
              disabled={isEdit || submitting}
            >
              <SelectTrigger id="type" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="newapi">NewAPI</SelectItem>
                <SelectItem value="sub2api">Sub2API</SelectItem>
              </SelectContent>
            </Select>
            {isEdit ? (
              <p className="text-[11px] text-muted-foreground">类型创建后不可修改</p>
            ) : null}
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="site_url">站点地址</Label>
            <Input
              id="site_url"
              placeholder="https://example.com"
              value={form.site_url}
              onChange={(e) => setForm({ ...form, site_url: e.target.value })}
              required
              disabled={submitting}
            />
          </div>

          <div className="space-y-1.5">
            <Label htmlFor="remark">备注（可选）</Label>
            <Input
              id="remark"
              value={form.remark}
              onChange={(e) => setForm({ ...form, remark: e.target.value })}
              placeholder="例如：赠送额度 / 主力账号"
              maxLength={512}
              disabled={submitting}
            />
          </div>

          <div className="space-y-1.5">
            <Label>余额管理</Label>
            <div className="grid grid-cols-2 gap-2 rounded-lg border border-border p-1" role="group" aria-label="余额管理模式">
              <button type="button" disabled={submitting} onClick={() => setForm({ ...form, balance_mode: "auto" })} className={cn("rounded-md px-3 py-2 text-xs font-medium transition-colors", !isManualBalance ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}>自动读取</button>
              <button type="button" disabled={submitting} onClick={() => setForm({ ...form, balance_mode: "manual" })} className={cn("rounded-md px-3 py-2 text-xs font-medium transition-colors", isManualBalance ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}>手动管理</button>
            </div>
            <p className="text-[11px] text-muted-foreground">{isManualBalance ? "直接使用手动当前余额；赠送或初始额度不会计入成本。" : "从上游读取余额和可见分组。"}</p>
            {isManualBalance ? (
              <div className="mt-2 space-y-1.5">
                <Label htmlFor="manual-balance">当前余额（修改后作为新的扣减起点）</Label>
                <Input
                  id="manual-balance"
                  type="number"
                  min="0"
                  step="any"
                  value={form.manual_balance}
                  onChange={(e) => setForm({ ...form, manual_balance: e.target.value })}
                  disabled={submitting}
                />
              </div>
            ) : null}
          </div>

          {/* 凭据类型 toggle */}
          {!isManualBalance ? <div className="space-y-1.5">
            <Label>凭据类型</Label>
            <div className="grid grid-cols-2 gap-2 rounded-lg border border-border p-1">
              <button
                type="button"
                disabled={submitting}
                onClick={() => setForm({ ...form, credential_mode: "password" })}
                className={cn(
                  "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                  !isTokenMode
                    ? "bg-foreground text-background"
                    : "text-muted-foreground hover:bg-muted",
                )}
              >
                账号密码
              </button>
              <button
                type="button"
                disabled={submitting}
                onClick={() => setForm({ ...form, credential_mode: "token" })}
                className={cn(
                  "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
                  isTokenMode
                    ? "bg-foreground text-background"
                    : "text-muted-foreground hover:bg-muted",
                )}
              >
                Token (跳过登录)
              </button>
            </div>
            <p className="text-[11px] text-muted-foreground">
              {isTokenMode
                ? form.type === "sub2api"
                  ? "填写 Access Token + Refresh Token，系统会在过期前自动刷新并保存新 Token。"
                  : "可使用 Cookie + User ID，或长期有效的访问令牌；访问令牌 Headers 可自定义。"
                : "提供账号密码，系统自动登录并续期。可能需要配打码 provider。"}
            </p>
          </div> : null}

          {/* —— password 模式字段 —— */}
          {!isManualBalance && !isTokenMode ? (
            <>
              <div className="grid grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label htmlFor="gatewayops-channel-username">账号 / 邮箱</Label>
                  <Input
                    id="gatewayops-channel-username"
                    name="gatewayops-channel-username"
                    autoComplete="off"
                    data-lpignore="true"
                    value={form.username}
                    onChange={(e) => setForm({ ...form, username: e.target.value })}
                    required
                    disabled={submitting}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="gatewayops-channel-password">
                    {isEdit ? "新密码 (留空不变)" : "密码"}
                  </Label>
                  <Input
                    id="gatewayops-channel-password"
                    name="gatewayops-channel-secret"
                    type="password"
                    autoComplete="new-password"
                    data-lpignore="true"
                    value={form.password}
                    onChange={(e) => setForm({ ...form, password: e.target.value })}
                    required={!isEdit || modeChanged}
                    disabled={submitting}
                  />
                </div>
              </div>
              <div className="space-y-2 rounded-lg border border-border p-3">
                <div className="flex items-center justify-between gap-2">
                  <div>
                    <p className="text-xs font-medium">登录请求配置</p>
                    <p className="text-[11px] text-muted-foreground">支持变量：{'{{username}}'}、{'{{password}}'}、{'{{turnstile_token}}'}</p>
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="h-8 text-xs"
                    disabled={submitting}
                    onClick={() => {
                      const defaults = defaultLoginConfig(form.type)
                      setForm({ ...form, login_headers: defaults.headers, login_params: defaults.params })
                    }}
                  >
                    恢复默认
                  </Button>
                </div>
                <RequestKVEditor title="Headers" items={form.login_headers} disabled={submitting} onChange={(items) => setForm({ ...form, login_headers: items })} />
                <RequestKVEditor title="参数" items={form.login_params} disabled={submitting} onChange={(items) => setForm({ ...form, login_params: items })} />
              </div>
            </>
          ) : null}

          {/* —— token 模式字段 —— */}
          {!isManualBalance && isTokenMode ? (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="username-display">备注（可选）</Label>
                <Input
                  id="username-display"
                  name="gatewayops-channel-username-display"
                  autoComplete="off"
                  data-lpignore="true"
                  placeholder="如：worry@example.com"
                  value={form.username}
                  onChange={(e) => setForm({ ...form, username: e.target.value })}
                  disabled={submitting}
                />
                <p className="text-[11px] text-muted-foreground">
                  仅作展示，不参与鉴权
                </p>
              </div>

              {form.type === "newapi" ? (
                <>
                  <div className="space-y-1.5">
                    <Label>NewAPI Token 方式</Label>
                    <div className="grid grid-cols-2 gap-2 rounded-md border border-border p-1">
                      <button
                        type="button"
                        disabled={submitting}
                        onClick={() => setForm({ ...form, newapi_auth_type: "cookie" })}
                        className={cn("rounded px-2 py-1.5 text-xs font-medium transition-colors", form.newapi_auth_type === "cookie" ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}
                      >
                        Cookie + User ID
                      </button>
                      <button
                        type="button"
                        disabled={submitting}
                        onClick={() => setForm({ ...form, newapi_auth_type: "access_token" })}
                        className={cn("rounded px-2 py-1.5 text-xs font-medium transition-colors", form.newapi_auth_type === "access_token" ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}
                      >
                        访问令牌
                      </button>
                    </div>
                  </div>
                  {form.newapi_auth_type === "cookie" ? (
                    <>
                      <div className="space-y-1.5">
                        <div className="flex items-center justify-between">
                          <Label htmlFor="newapi-cookie">Cookie</Label>
                          <NewAPITokenHelp />
                        </div>
                        <Textarea
                          id="newapi-cookie"
                          placeholder={isEdit ? "留空 = 不修改；填写则覆盖原 token" : "粘贴整段 Cookie 字符串，例：session=...; ..."}
                          value={form.newapi_cookie}
                          onChange={(e) => setForm({ ...form, newapi_cookie: e.target.value })}
                          rows={3}
                          className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono"
                          disabled={submitting}
                        />
                      </div>
                      <div className="space-y-1.5">
                        <Label htmlFor="newapi-user-id">User ID</Label>
                        <Input
                          id="newapi-user-id"
                          placeholder={isEdit ? "留空 = 不修改；NewAPI 个人设置页可见" : "整数，NewAPI 个人设置页可见"}
                          value={form.newapi_user_id}
                          onChange={(e) => setForm({ ...form, newapi_user_id: e.target.value })}
                          disabled={submitting}
                        />
                      </div>
                    </>
                  ) : (
                    <>
                      <div className="space-y-1.5">
                        <Label htmlFor="newapi-access-token">访问令牌</Label>
                        <Textarea
                          id="newapi-access-token"
                          placeholder={isEdit ? "留空 = 不修改；填写则覆盖原访问令牌" : "粘贴长期有效的访问令牌"}
                          value={form.newapi_access_token}
                          onChange={(e) => setForm({ ...form, newapi_access_token: e.target.value })}
                          rows={3}
                          className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono"
                          disabled={submitting}
                        />
                      </div>
                      <div className="space-y-2 rounded-lg border border-border p-3">
                        <div className="flex items-center justify-between gap-2">
                          <div>
                            <p className="text-xs font-medium">访问令牌 Headers</p>
                            <p className="text-[11px] text-muted-foreground">默认 Authorization = Bearer {'{{token}}'}，可增删改；直接写入真实 Token 时，重新编辑会仅对 Token 本身脱敏。</p>
                          </div>
                          <Button type="button" variant="outline" size="sm" className="h-8 text-xs" disabled={submitting} onClick={() => setForm({ ...form, newapi_token_headers: defaultNewAPITokenHeaders() })}>恢复默认</Button>
                        </div>
                        <RequestKVEditor title="Headers" items={form.newapi_token_headers} disabled={submitting} onChange={(items) => setForm({ ...form, newapi_token_headers: items })} />
                      </div>
                    </>
                  )}
                </>
              ) : null}

              {form.type === "sub2api" ? (
                <>
                  <div className="space-y-1.5">
                    <div className="flex items-center justify-between">
                      <Label htmlFor="sub2api-token">Access Token</Label>
                      <Sub2APITokenHelp />
                    </div>
                    <Textarea
                      id="sub2api-token"
                      placeholder={isEdit ? "留空 = 不修改；填写则覆盖原 token" : "粘贴 access_token"}
                      value={form.sub2api_access_token}
                      onChange={(e) => setForm({ ...form, sub2api_access_token: e.target.value })}
                      rows={3}
                      className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono"
                      disabled={submitting}
                    />
                  </div>
                  <div className="space-y-1.5">
                    <Label htmlFor="sub2api-refresh-token">Refresh Token</Label>
                    <Textarea
                      id="sub2api-refresh-token"
                      placeholder={isEdit ? "留空 = 不修改；填写时请与 Access Token 一起更新" : "粘贴 refresh_token"}
                      value={form.sub2api_refresh_token}
                      onChange={(e) => setForm({ ...form, sub2api_refresh_token: e.target.value })}
                      rows={3}
                      className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono"
                      disabled={submitting}
                    />
                    <p className="text-[11px] text-muted-foreground">系统会在 Access Token 过期前调用 /api/v1/auth/refresh，并自动保存返回的新 Token。</p>
                  </div>
                </>
              ) : null}
            </>
          ) : null}

          {!isManualBalance ? (
            <AdditionalAccountsEditor
              type={form.type}
              accounts={form.additional_accounts}
              captchas={captchas.data ?? []}
              disabled={submitting}
              onChange={(accounts) => setForm({ ...form, additional_accounts: accounts })}
            />
          ) : null}

          <div className="space-y-1.5">
            <Label htmlFor="threshold">余额阈值（低于此值发告警，0 = 不告警）</Label>
            <Input
              id="threshold"
              type="number"
              step="0.01"
              min="0"
              value={form.balance_threshold}
              onChange={(e) => setForm({ ...form, balance_threshold: e.target.value })}
              disabled={submitting}
            />
          </div>

          <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
            <div>
              <p className="text-sm font-medium">启用监控</p>
              <p className="text-xs text-muted-foreground">关闭后调度器不会扫描此渠道</p>
            </div>
            <Switch
              checked={form.monitor_enabled}
              onCheckedChange={(v) => setForm({ ...form, monitor_enabled: v })}
              disabled={submitting}
            />
          </div>

          {/* Turnstile / 打码：token 模式下整段不展示 */}
          {!isManualBalance && !isTokenMode ? (
            <>
              <div className="flex items-center justify-between rounded-lg border border-border px-3 py-2">
                <div>
                  <p className="text-sm font-medium">Turnstile 人机校验</p>
                  <p className="text-xs text-muted-foreground">站点开启 Cloudflare Turnstile 时打开</p>
                </div>
                <Switch
                  checked={form.turnstile_enabled}
                  onCheckedChange={(v) => setForm({ ...form, turnstile_enabled: v })}
                  disabled={submitting}
                />
              </div>

              {form.turnstile_enabled ? (
                <div className="space-y-1.5">
                  <Label htmlFor="captcha-config">打码 provider</Label>
                  <Select
                    value={form.captcha_config_id}
                    onValueChange={(v) => setForm({ ...form, captcha_config_id: v })}
                    disabled={submitting}
                  >
                    <SelectTrigger id="captcha-config" className="w-full">
                      <SelectValue
                        placeholder={
                          captchas.data && captchas.data.length > 0
                            ? "选择 provider"
                            : "先到底部 [验证码服务] 卡片新增"
                        }
                      />
                    </SelectTrigger>
                    <SelectContent>
                      {(captchas.data ?? [])
                        .filter((c) => c.enabled)
                        .map((c) => (
                          <SelectItem key={c.id} value={String(c.id)}>
                            {c.name}
                          </SelectItem>
                        ))}
                    </SelectContent>
                  </Select>
                  <p className="text-[11px] text-muted-foreground">
                    {"siteKey 会自动从上游公开接口拉取，无需在此填写。"}
                  </p>
                </div>
              ) : null}
            </>
          ) : null}

          {error ? (
            <p className="text-sm text-destructive" role="alert">
              {error}
            </p>
          ) : null}

          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
              取消
            </Button>
            <Button type="submit" disabled={submitting}>
              {submitting ? "保存中…" : "保存"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function RequestKVEditor({
  title,
  items,
  disabled,
  onChange,
}: {
  title: string
  items: RequestKV[]
  disabled: boolean
  onChange: (items: RequestKV[]) => void
}) {
  function patch(index: number, update: Partial<RequestKV>) {
    onChange(items.map((item, current) => current === index ? { ...item, ...update } : item))
  }

  return (
    <div className="space-y-1.5">
      <div className="flex items-center justify-between gap-2">
        <Label>{title}</Label>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-7 gap-1 px-2 text-[11px]"
          disabled={disabled}
          onClick={() => onChange([...items, { key: "", value: "" }])}
        >
          <Plus className="size-3" />添加
        </Button>
      </div>
      <div className="space-y-1.5">
        {items.length === 0 ? (
          <p className="rounded-md border border-dashed border-border px-3 py-2 text-[11px] text-muted-foreground">未配置，可点击添加。</p>
        ) : items.map((item, index) => (
          <div key={`${title}-${index}`} className="grid grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)_32px] gap-1.5">
            <Input
              aria-label={`${title} Key ${index + 1}`}
              value={item.key}
              onChange={(event) => patch(index, { key: event.target.value })}
              placeholder="Key"
              className="h-8 font-mono text-xs"
              disabled={disabled}
            />
            <Input
              aria-label={`${title} Value ${index + 1}`}
              value={item.value}
              onChange={(event) => patch(index, { value: event.target.value })}
              placeholder="Value"
              className="h-8 font-mono text-xs"
              disabled={disabled}
            />
            <button
              type="button"
              aria-label={`删除 ${title} 第 ${index + 1} 项`}
              title="删除"
              disabled={disabled}
              onClick={() => onChange(items.filter((_, current) => current !== index))}
              className="inline-flex size-8 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive disabled:pointer-events-none disabled:opacity-50"
            >
              <Trash2 className="size-3.5" />
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

function AdditionalAccountsEditor({
  type,
  accounts,
  captchas,
  disabled,
  onChange,
}: {
  type: ChannelType
  accounts: AdditionalAccountForm[]
  captchas: CaptchaConfig[]
  disabled: boolean
  onChange: (accounts: AdditionalAccountForm[]) => void
}) {
  function patch(index: number, update: Partial<AdditionalAccountForm>) {
    onChange(accounts.map((account, current) => current === index ? { ...account, ...update } : account))
  }

  return (
    <div className="space-y-2 border-t border-border pt-3">
      <div className="flex items-center justify-between gap-3">
        <div>
          <Label>附加账号</Label>
          <p className="mt-1 text-[11px] text-muted-foreground">与主账号共用站点和渠道配置，余额独立读取后汇总。</p>
        </div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-9 shrink-0 gap-1.5 text-xs"
          disabled={disabled}
          onClick={() => onChange([...accounts, emptyAdditionalAccount()])}
        >
          <Plus className="size-3.5" />添加账号
        </Button>
      </div>

      {accounts.map((account, index) => {
        const isToken = account.credential_mode === "token"
        const modeChanged = account.id != null && account.credential_mode !== account.initial_credential_mode
        return (
          <fieldset key={account.id ?? `new-${index}`} className="space-y-3 rounded-md border border-border bg-muted/20 p-3">
            <div className="flex items-center justify-between gap-3">
              <p className="text-xs font-semibold text-foreground">账号 {index + 2}</p>
              <button
                type="button"
                aria-label={`移除账号 ${index + 2}`}
                title="移除账号"
                disabled={disabled}
                onClick={() => onChange(accounts.filter((_, current) => current !== index))}
                className="inline-flex size-8 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-destructive/10 hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50"
              >
                <Trash2 className="size-3.5" />
              </button>
            </div>

            <div className="space-y-1.5">
              <Label htmlFor={`additional-account-${index}`}>账号 / 展示名称</Label>
              <Input
                id={`additional-account-${index}`}
                value={account.username}
                onChange={(event) => patch(index, { username: event.target.value })}
                placeholder="例如：account-02@example.com"
                required
                disabled={disabled}
              />
            </div>

            <div className="space-y-1.5">
              <Label>凭据类型</Label>
              <div className="grid grid-cols-2 gap-2 rounded-md border border-border p-1" role="group" aria-label={`账号 ${index + 2} 凭据类型`}>
                <button type="button" disabled={disabled} onClick={() => patch(index, { credential_mode: "password" })} className={cn("rounded px-2 py-1.5 text-xs font-medium transition-colors", !isToken ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}>账号密码</button>
                <button type="button" disabled={disabled} onClick={() => patch(index, { credential_mode: "token" })} className={cn("rounded px-2 py-1.5 text-xs font-medium transition-colors", isToken ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}>Token</button>
              </div>
            </div>

            {!isToken ? (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor={`additional-password-${index}`}>{account.id != null && !modeChanged ? "新密码（留空不变）" : "密码"}</Label>
                  <Input
                    id={`additional-password-${index}`}
                    type="password"
                    autoComplete="new-password"
                    value={account.password}
                    onChange={(event) => patch(index, { password: event.target.value })}
                    required={account.id == null || modeChanged}
                    disabled={disabled}
                  />
                </div>
                <div className="flex items-center justify-between rounded-md border border-border px-3 py-2">
                  <Label htmlFor={`additional-turnstile-${index}`} className="text-xs">Turnstile 人机校验</Label>
                  <Switch id={`additional-turnstile-${index}`} checked={account.turnstile_enabled} onCheckedChange={(value) => patch(index, { turnstile_enabled: value })} disabled={disabled} />
                </div>
                {account.turnstile_enabled ? (
                  <div className="space-y-1.5">
                    <Label>打码 provider</Label>
                    <Select value={account.captcha_config_id} onValueChange={(value) => patch(index, { captcha_config_id: value })} disabled={disabled}>
                      <SelectTrigger className="w-full"><SelectValue placeholder="选择 provider" /></SelectTrigger>
                      <SelectContent>
                        {captchas.filter((captcha) => captcha.enabled).map((captcha) => <SelectItem key={captcha.id} value={String(captcha.id)}>{captcha.name}</SelectItem>)}
                      </SelectContent>
                    </Select>
                  </div>
                ) : null}
              </>
            ) : type === "newapi" ? (
              <>
                <div className="space-y-1.5">
                  <Label>NewAPI Token 方式</Label>
                  <div className="grid grid-cols-2 gap-2 rounded-md border border-border p-1">
                    <button type="button" disabled={disabled} onClick={() => patch(index, { newapi_auth_type: "cookie" })} className={cn("rounded px-2 py-1.5 text-xs font-medium transition-colors", account.newapi_auth_type === "cookie" ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}>Cookie + User ID</button>
                    <button type="button" disabled={disabled} onClick={() => patch(index, { newapi_auth_type: "access_token" })} className={cn("rounded px-2 py-1.5 text-xs font-medium transition-colors", account.newapi_auth_type === "access_token" ? "bg-foreground text-background" : "text-muted-foreground hover:bg-muted")}>访问令牌</button>
                  </div>
                </div>
                {account.newapi_auth_type === "cookie" ? (
                  <>
                    <div className="space-y-1.5">
                      <Label htmlFor={`additional-cookie-${index}`}>Cookie</Label>
                      <Textarea id={`additional-cookie-${index}`} rows={3} className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono" placeholder={account.id != null && !modeChanged ? "留空不变；填写则覆盖" : "粘贴 Cookie"} value={account.newapi_cookie} onChange={(event) => patch(index, { newapi_cookie: event.target.value })} disabled={disabled} />
                    </div>
                    <div className="space-y-1.5">
                      <Label htmlFor={`additional-user-id-${index}`}>User ID</Label>
                      <Input id={`additional-user-id-${index}`} placeholder={account.id != null && !modeChanged ? "留空不变；填写则覆盖" : "NewAPI 用户 ID"} value={account.newapi_user_id} onChange={(event) => patch(index, { newapi_user_id: event.target.value })} disabled={disabled} />
                    </div>
                  </>
                ) : (
                  <>
                    <div className="space-y-1.5">
                      <Label htmlFor={`additional-newapi-access-token-${index}`}>访问令牌</Label>
                      <Textarea id={`additional-newapi-access-token-${index}`} rows={3} className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono" placeholder={account.id != null && !modeChanged ? "留空不变；填写则覆盖" : "粘贴访问令牌"} value={account.newapi_access_token} onChange={(event) => patch(index, { newapi_access_token: event.target.value })} disabled={disabled} />
                    </div>
                    <div className="space-y-2 rounded-md border border-border p-2.5">
                      <div className="flex items-center justify-between gap-2">
                        <div>
                          <p className="text-xs font-medium">访问令牌 Headers</p>
                          <p className="text-[11px] text-muted-foreground">直接写入真实 Token 时，重新编辑会仅对 Token 本身脱敏。</p>
                        </div>
                        <Button type="button" variant="outline" size="sm" className="h-7 text-[11px]" disabled={disabled} onClick={() => patch(index, { newapi_token_headers: defaultNewAPITokenHeaders() })}>恢复默认</Button>
                      </div>
                      <RequestKVEditor title="Headers" items={account.newapi_token_headers} disabled={disabled} onChange={(items) => patch(index, { newapi_token_headers: items })} />
                    </div>
                  </>
                )}
              </>
            ) : (
              <>
                <div className="space-y-1.5">
                  <Label htmlFor={`additional-token-${index}`}>Access Token</Label>
                  <Textarea id={`additional-token-${index}`} rows={3} className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono" placeholder={account.id != null && !modeChanged ? "留空不变；填写则覆盖" : "粘贴 access_token"} value={account.sub2api_access_token} onChange={(event) => patch(index, { sub2api_access_token: event.target.value })} disabled={disabled} />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor={`additional-refresh-token-${index}`}>Refresh Token</Label>
                  <Textarea id={`additional-refresh-token-${index}`} rows={3} className="field-sizing-fixed min-w-0 max-w-full resize-y text-xs font-mono" placeholder={account.id != null && !modeChanged ? "留空不变；填写时与 Access Token 一起更新" : "粘贴 refresh_token"} value={account.sub2api_refresh_token} onChange={(event) => patch(index, { sub2api_refresh_token: event.target.value })} disabled={disabled} />
                </div>
              </>
            )}
          </fieldset>
        )
      })}
    </div>
  )
}

/**
 * NewAPITokenHelp 是 Cookie / User ID 的获取指引浮窗。
 * 用 Popover 而不是新页面 / 新对话框，避免在表单流程中打断用户。
 */
function NewAPITokenHelp() {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
        >
          <HelpCircle className="size-3" />
          如何获取？
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80 text-xs" align="end">
        <p className="font-medium text-foreground">获取 Cookie</p>
        <ol className="mt-1 ml-4 list-decimal space-y-0.5 text-muted-foreground">
          <li>在浏览器登录 NewAPI 站点</li>
          <li>按 F12 打开 DevTools，切到 Application / 存储 标签</li>
          <li>左侧 Cookies 选中站点域名</li>
          <li>复制 <span className="font-mono text-foreground">session</span> 字段值，格式：<span className="font-mono">session=xxxxx</span></li>
        </ol>
        <p className="mt-2 font-medium text-foreground">获取 User ID</p>
        <p className="mt-1 text-muted-foreground">
          登录 NewAPI 后到「个人设置」页，页面上会显示用户 ID（整数）。或在 URL <span className="font-mono">/user</span> 后的数字。
        </p>
      </PopoverContent>
    </Popover>
  )
}

function Sub2APITokenHelp() {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground"
        >
          <HelpCircle className="size-3" />
          如何获取？
        </button>
      </PopoverTrigger>
      <PopoverContent className="w-80 text-xs" align="end">
        <p className="font-medium text-foreground">获取 Access Token</p>
        <ol className="mt-1 ml-4 list-decimal space-y-0.5 text-muted-foreground">
          <li>在浏览器登录 Sub2API 站点</li>
          <li>按 F12 打开 DevTools，切到 Application / 存储 标签</li>
          <li>左侧 Local Storage 选中站点域名</li>
          <li>找到 <span className="font-mono text-foreground">access_token</span> 字段并复制</li>
        </ol>
        <p className="mt-2 font-medium text-foreground">获取 Refresh Token</p>
        <p className="mt-1 text-muted-foreground">
          在同一 Local Storage 中找到 <span className="font-mono text-foreground">refresh_token</span> 并复制。Access Token 与 Refresh Token 建议成对填写。
        </p>
        <p className="mt-2 text-[11px] text-muted-foreground">
          Access Token 也可以从 Network 请求的 <span className="font-mono">Authorization</span> 头获取，填写时去掉 <span className="font-mono">Bearer </span> 前缀。
        </p>
      </PopoverContent>
    </Popover>
  )
}
