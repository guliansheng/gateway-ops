import * as React from 'react'
import * as TooltipPrimitive from '@radix-ui/react-tooltip'

import { cn } from '@/lib/utils'

type TooltipInteractionContextValue = {
  interactionId: string
  togglePinned: () => void
}

const TooltipInteractionContext = React.createContext<TooltipInteractionContextValue | null>(null)

function TooltipProvider({
  delayDuration = 0,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Provider>) {
  return (
    <TooltipPrimitive.Provider
      data-slot="tooltip-provider"
      delayDuration={delayDuration}
      {...props}
    />
  )
}

function Tooltip({
  open: controlledOpen,
  defaultOpen = false,
  onOpenChange,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Root>) {
  const [uncontrolledOpen, setUncontrolledOpen] = React.useState(defaultOpen)
  const [pinned, setPinned] = React.useState(false)
  const pinnedRef = React.useRef(false)
  const interactionId = React.useId()
  const open = controlledOpen ?? uncontrolledOpen
  const openRef = React.useRef(open)
  openRef.current = open

  const updateOpen = React.useCallback((nextOpen: boolean) => {
    if (openRef.current === nextOpen) return
    openRef.current = nextOpen
    if (controlledOpen === undefined) setUncontrolledOpen(nextOpen)
    onOpenChange?.(nextOpen)
  }, [controlledOpen, onOpenChange])

  const setPinnedOpen = React.useCallback((nextPinned: boolean) => {
    pinnedRef.current = nextPinned
    setPinned(nextPinned)
    updateOpen(nextPinned)
  }, [updateOpen])

  const togglePinned = React.useCallback(() => {
    setPinnedOpen(!pinnedRef.current)
  }, [setPinnedOpen])

  const handleOpenChange = React.useCallback((nextOpen: boolean) => {
    if (!nextOpen && pinnedRef.current) return
    updateOpen(nextOpen)
  }, [updateOpen])

  React.useEffect(() => {
    if (!pinned) return

    const closeOnOutsideClick = (event: PointerEvent) => {
      const isCurrentTooltip = event.composedPath().some((target) => (
        target instanceof HTMLElement
        && target.dataset.tooltipInteractionId === interactionId
      ))
      if (!isCurrentTooltip) setPinnedOpen(false)
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setPinnedOpen(false)
    }

    document.addEventListener('pointerdown', closeOnOutsideClick, true)
    document.addEventListener('keydown', closeOnEscape)
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsideClick, true)
      document.removeEventListener('keydown', closeOnEscape)
    }
  }, [interactionId, pinned, setPinnedOpen])

  const interaction = React.useMemo(() => ({ interactionId, togglePinned }), [interactionId, togglePinned])

  return (
    <TooltipProvider>
      <TooltipInteractionContext.Provider value={interaction}>
        <TooltipPrimitive.Root
          data-slot="tooltip"
          open={open}
          onOpenChange={handleOpenChange}
          {...props}
        />
      </TooltipInteractionContext.Provider>
    </TooltipProvider>
  )
}

function TooltipTrigger({
  onClick,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Trigger>) {
  const interaction = React.useContext(TooltipInteractionContext)
  return (
    <TooltipPrimitive.Trigger
      data-slot="tooltip-trigger"
      data-tooltip-interaction-id={interaction?.interactionId}
      onClick={(event) => {
        onClick?.(event)
        if (!event.defaultPrevented) interaction?.togglePinned()
      }}
      {...props}
    />
  )
}

function TooltipContent({
  className,
  sideOffset = 0,
  children,
  ...props
}: React.ComponentProps<typeof TooltipPrimitive.Content>) {
  const interaction = React.useContext(TooltipInteractionContext)
  return (
    <TooltipPrimitive.Portal>
      <TooltipPrimitive.Content
        data-slot="tooltip-content"
        data-tooltip-interaction-id={interaction?.interactionId}
        sideOffset={sideOffset}
        className={cn(
          'bg-foreground text-background animate-in fade-in-0 zoom-in-95 data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=closed]:zoom-out-95 data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2 data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2 z-50 w-fit origin-(--radix-tooltip-content-transform-origin) rounded-md px-3 py-1.5 text-xs text-balance',
          className,
        )}
        {...props}
      >
        {children}
      </TooltipPrimitive.Content>
    </TooltipPrimitive.Portal>
  )
}

export { Tooltip, TooltipTrigger, TooltipContent, TooltipProvider }
