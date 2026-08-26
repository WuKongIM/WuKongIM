import { useEffect, useId, useRef, type RefObject } from "react"

const focusableSelector = "button:not([disabled]),[href],input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex='-1'])"
const openModalIds: string[] = []

export function useModalFocus(
  open: boolean,
  containerRef: RefObject<HTMLElement | null>,
  onClose: () => void,
  initialFocusSelector?: string,
) {
  const modalId = useId()
  const onCloseRef = useRef(onClose)

  useEffect(() => {
    onCloseRef.current = onClose
  })

  useEffect(() => {
    if (!open) {
      return
    }

    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const container = containerRef.current
    const preferred = initialFocusSelector
      ? container?.querySelector<HTMLElement>(initialFocusSelector)
      : null
    const focusable = container?.querySelectorAll<HTMLElement>(focusableSelector)
    openModalIds.push(modalId)
    ;(preferred ?? focusable?.[0] ?? container)?.focus()

    const onKeyDown = (event: KeyboardEvent) => {
      if (openModalIds.at(-1) !== modalId) {
        return
      }
      if (event.key === "Escape") {
        event.preventDefault()
        onCloseRef.current()
        return
      }
      if (event.key !== "Tab" || !focusable?.length) {
        return
      }
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener("keydown", onKeyDown)
    return () => {
      document.removeEventListener("keydown", onKeyDown)
      const modalIndex = openModalIds.lastIndexOf(modalId)
      if (modalIndex >= 0) {
        openModalIds.splice(modalIndex, 1)
      }
      previousFocus?.focus()
    }
  }, [containerRef, initialFocusSelector, modalId, open])
}
