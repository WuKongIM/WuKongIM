import { useEffect, useState, type ReactNode } from "react"
import { Navigate, useLocation } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { getPermissions } from "@/lib/manager-api"
import { defaultAppPath } from "@/lib/navigation"

type RouteGateProps = {
  children: ReactNode
}

export function ProtectedRoute({ children }: RouteGateProps) {
  const isHydrated = useAuthStore((state) => state.isHydrated)
  const status = useAuthStore((state) => state.status)
  const location = useLocation()
  const [probeComplete, setProbeComplete] = useState(false)

  useEffect(() => {
    if (!isHydrated || status !== "anonymous") {
      return
    }

    let cancelled = false
    void getPermissions()
      .then((snapshot) => {
        if (!cancelled && !snapshot.auth_enabled) {
          useAuthStore.getState().enterAuthDisabledReadonly()
        }
      })
      .catch(() => {
        // Authentication-enabled managers reject this anonymous probe.
      })
      .finally(() => {
        if (!cancelled) {
          setProbeComplete(true)
        }
      })

    return () => {
      cancelled = true
    }
  }, [isHydrated, status])

  if (!isHydrated) {
    return null
  }

  if (status === "readonly") {
    return location.pathname === "/cluster/backups"
      ? <>{children}</>
      : <Navigate replace to="/cluster/backups" />
  }

  if (status !== "authenticated") {
    if (!probeComplete) {
      return null
    }
    return <Navigate replace to="/login" />
  }

  return <>{children}</>
}

export function PublicOnlyRoute({ children }: RouteGateProps) {
  const isHydrated = useAuthStore((state) => state.isHydrated)
  const status = useAuthStore((state) => state.status)

  if (!isHydrated) {
    return null
  }

  if (status === "authenticated") {
    return <Navigate replace to={defaultAppPath} />
  }

  if (status === "readonly") {
    return <Navigate replace to="/cluster/backups" />
  }

  return <>{children}</>
}
