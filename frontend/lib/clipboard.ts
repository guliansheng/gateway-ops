/**
 * Copies text with a legacy fallback.
 *
 * `navigator.clipboard` only exists in secure contexts (HTTPS or localhost) and
 * can still reject when the document is not focused or an embedding frame
 * denies the permission. Plain-HTTP pages therefore fall back to a hidden
 * textarea plus `document.execCommand("copy")`; the boolean result lets callers
 * show feedback instead of failing silently.
 */
export async function copyText(value: string): Promise<boolean> {
  if (!value) return false
  if (typeof window !== "undefined" && window.isSecureContext && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(value)
      return true
    } catch {
      // Permission denied or document not focused: fall through.
    }
  }
  return legacyCopy(value)
}

function legacyCopy(value: string): boolean {
  if (typeof document === "undefined" || !document.body) return false
  const area = document.createElement("textarea")
  area.value = value
  area.setAttribute("readonly", "")
  area.setAttribute("aria-hidden", "true")
  area.style.cssText = "position:fixed;top:0;left:-9999px;opacity:0"
  document.body.append(area)
  const selection = document.getSelection()
  const previous = selection && selection.rangeCount ? selection.getRangeAt(0) : null
  let copied = false
  try {
    area.select()
    copied = document.execCommand("copy")
  } catch {
    copied = false
  } finally {
    area.remove()
    if (previous && selection) {
      selection.removeAllRanges()
      selection.addRange(previous)
    }
  }
  return copied
}
