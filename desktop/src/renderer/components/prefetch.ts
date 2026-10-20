import { useCallback, useEffect, useRef } from 'react'

/**
 * How long the pointer rests on a file before its body is fetched. Long enough
 * that sweeping across a list asks for nothing; short enough that by the time
 * the click lands, the bytes usually have (spec 65, 2.3).
 */
export const HOVER_PREFETCH_MS = 80

/** Pointer handlers that prefetch a path once the pointer has rested on it. */
export function useHoverPrefetch(prefetch: ((path: string) => void) | undefined): {
  enter: (path: string) => void
  leave: () => void
} {
  const timer = useRef<number | null>(null)
  const leave = useCallback(() => {
    if (timer.current !== null) window.clearTimeout(timer.current)
    timer.current = null
  }, [])
  const enter = useCallback(
    (path: string) => {
      leave()
      if (!prefetch) return
      timer.current = window.setTimeout(() => prefetch(path), HOVER_PREFETCH_MS)
    },
    [prefetch, leave],
  )
  useEffect(() => leave, [leave])
  return { enter, leave }
}
