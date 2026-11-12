"use client"

import { useEffect, useRef, useState } from "react"
import { useLocation, useNavigate } from "react-router-dom"
import {
  Bell,
  LayoutDashboard,
  Plus,
  Settings,
  ShieldCheck,
  type LucideIcon,
} from "lucide-react"
import { cn } from "@/lib/utils"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { useAddChannel } from "@/lib/add-channel-context"

interface DockItem {
  icon: LucideIcon
  label: string
  /** 走路由的项：点了 navigate(path)；不传则视为动作型（onAction）。 */
  path?: string
  /** 动作型项（如 +新增渠道）。 */
  onAction?: () => void
  gradient: string
}

const BASE_SIZE = 52
const MAX_SCALE = 1.6
const INFLUENCE_RADIUS = 130

// macOS-style magnification: half-cosine — sharp at the center, gentle at the edges
function magnification(dist: number): number {
  if (dist >= INFLUENCE_RADIUS) return 1
  const t = dist / INFLUENCE_RADIUS
  const falloff = Math.cos((t * Math.PI) / 2)
  return 1 + (MAX_SCALE - 1) * falloff
}

function DockIcon({
  item,
  mouseX,
  active,
  onClick,
}: {
  item: DockItem
  mouseX: number | null
  active: boolean
  onClick: () => void
}) {
  const ref = useRef<HTMLButtonElement>(null)
  const [pressed, setPressed] = useState(false)
  const isTracking = mouseX !== null

  let scale = 1
  if (mouseX !== null && ref.current) {
    const rect = ref.current.getBoundingClientRect()
    const center = rect.left + rect.width / 2
    scale = magnification(Math.abs(mouseX - center))
  }

  const size = BASE_SIZE * scale
  const radius = size * 0.225
  const Icon = item.icon

  return (
    <div className="flex flex-col items-center">
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            ref={ref}
            onClick={onClick}
            onMouseDown={() => setPressed(true)}
            onMouseUp={() => setPressed(false)}
            onMouseLeave={() => setPressed(false)}
            className={cn(
              "relative flex items-center justify-center overflow-hidden",
              "bg-linear-to-b",
              "shadow-[0_6px_14px_-2px_rgba(0,0,0,0.28),0_2px_4px_-1px_rgba(0,0,0,0.15),inset_0_1px_0_rgba(255,255,255,0.55),inset_0_-2px_4px_rgba(0,0,0,0.14)]",
              "ring-1 ring-black/10",
              "will-change-transform cursor-pointer",
              item.gradient,
            )}
            style={{
              width: `${size}px`,
              height: `${size}px`,
              borderRadius: `${radius}px`,
              transform: pressed ? "scale(0.86)" : "scale(1)",
              transformOrigin: "center bottom",
              transitionProperty: "width, height, border-radius, transform",
              transitionDuration: pressed
                ? "80ms"
                : isTracking
                  ? "80ms"
                  : "320ms",
              transitionTimingFunction:
                pressed || isTracking
                  ? "cubic-bezier(0.22, 1, 0.36, 1)"
                  : "cubic-bezier(0.34, 1.56, 0.64, 1)",
            }}
            aria-label={item.label}
          >
            <span
              aria-hidden
              className="pointer-events-none absolute inset-x-0 top-0 bg-linear-to-b from-white/35 to-transparent"
              style={{
                height: "55%",
                borderTopLeftRadius: `${radius}px`,
                borderTopRightRadius: `${radius}px`,
              }}
            />
            <Icon
              className="relative text-white drop-shadow-[0_1px_1.5px_rgba(0,0,0,0.35)]"
              style={{
                width: `${size * 0.5}px`,
                height: `${size * 0.5}px`,
              }}
              strokeWidth={2.2}
            />
          </button>
        </TooltipTrigger>
        <TooltipContent
          side="top"
          sideOffset={16}
          className="rounded-md border border-white/20 bg-black/75 px-2.5 py-1 text-xs text-white shadow-md backdrop-blur-md"
        >
          {item.label}
        </TooltipContent>
      </Tooltip>
      <span
        className={cn(
          "mt-1 size-1.25 rounded-full transition-opacity duration-200",
          active ? "bg-foreground/70 opacity-100" : "opacity-0",
        )}
      />
    </div>
  )
}

export function DockBar() {
  const navigate = useNavigate()
  const location = useLocation()
  const { openAdd } = useAddChannel()

  const items: DockItem[] = [
    {
      icon: LayoutDashboard,
      label: "监控面板",
      path: "/",
      gradient: "from-sky-400 via-blue-500 to-blue-600",
    },
    {
      icon: Plus,
      label: "添加渠道",
      onAction: openAdd,
      gradient: "from-emerald-400 via-emerald-500 to-teal-600",
    },
    {
      icon: ShieldCheck,
      label: "打码平台",
      path: "/captcha",
      gradient: "from-fuchsia-400 via-purple-500 to-purple-700",
    },
    {
      icon: Bell,
      label: "通知渠道",
      path: "/notifications",
      gradient: "from-amber-300 via-orange-500 to-rose-500",
    },
    {
      icon: Settings,
      label: "系统设置",
      path: "/settings",
      gradient: "from-zinc-400 via-zinc-500 to-zinc-700",
    },
  ]

  const [mouseX, setMouseX] = useState<number | null>(null)
  const dockRef = useRef<HTMLDivElement>(null)
  const rafRef = useRef<number | null>(null)
  const pendingXRef = useRef<number | null>(null)

  useEffect(() => {
    return () => {
      if (rafRef.current !== null) cancelAnimationFrame(rafRef.current)
    }
  }, [])

  const handleMouseMove = (e: React.MouseEvent) => {
    pendingXRef.current = e.clientX
    if (rafRef.current === null) {
      rafRef.current = requestAnimationFrame(() => {
        rafRef.current = null
        if (pendingXRef.current !== null) setMouseX(pendingXRef.current)
      })
    }
  }

  const handleMouseLeave = () => {
    pendingXRef.current = null
    if (rafRef.current !== null) {
      cancelAnimationFrame(rafRef.current)
      rafRef.current = null
    }
    setMouseX(null)
  }

  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-4 z-50 flex justify-center">
      <TooltipProvider delayDuration={0}>
        <div
          ref={dockRef}
          onMouseMove={handleMouseMove}
          onMouseLeave={handleMouseLeave}
          className={cn(
            "pointer-events-auto flex items-end gap-2 px-3 pt-2 pb-1.5",
            "rounded-[22px]",
            "border border-white/40 dark:border-white/10",
            "bg-white/35 dark:bg-white/6",
            "backdrop-blur-2xl backdrop-saturate-[1.8]",
            "shadow-[0_18px_50px_-12px_rgba(0,0,0,0.35),0_2px_8px_rgba(0,0,0,0.12),inset_0_1px_0_rgba(255,255,255,0.6),inset_0_0_0_0.5px_rgba(255,255,255,0.3)]",
            "dark:shadow-[0_18px_50px_-12px_rgba(0,0,0,0.65),0_2px_8px_rgba(0,0,0,0.4),inset_0_1px_0_rgba(255,255,255,0.1),inset_0_0_0_0.5px_rgba(255,255,255,0.06)]",
          )}
        >
          {items.map((item) => {
            const active = item.path != null && location.pathname === item.path
            const onClick = () => {
              if (item.onAction) item.onAction()
              else if (item.path) navigate(item.path)
            }
            return (
              <DockIcon
                key={item.label}
                item={item}
                mouseX={mouseX}
                active={active}
                onClick={onClick}
              />
            )
          })}
        </div>
      </TooltipProvider>
    </div>
  )
}
