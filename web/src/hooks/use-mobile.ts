import * as React from "react"

const MOBILE_BREAKPOINT = 768
const MOBILE_QUERY = `(max-width: ${MOBILE_BREAKPOINT - 1}px)`

export function useIsMobile() {
  const subscribe = React.useCallback((notify: () => void) => {
    const mql = window.matchMedia(MOBILE_QUERY)
    mql.addEventListener("change", notify)
    return () => {
      mql.removeEventListener("change", notify)
    }
  }, [])

  const getSnapshot = React.useCallback(() => window.matchMedia(MOBILE_QUERY).matches, [])
  const getServerSnapshot = React.useCallback(() => false, [])
  return React.useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot)
}
