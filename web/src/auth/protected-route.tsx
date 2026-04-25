import type { ReactNode } from "react"
import { Navigate } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"

type RouteGateProps = {
  children: ReactNode
}

export function ProtectedRoute({ children }: RouteGateProps) {
  const isHydrated = useAuthStore((state) => state.isHydrated)
  const status = useAuthStore((state) => state.status)

  if (!isHydrated) {
    return null
  }

  if (status !== "authenticated") {
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
    return <Navigate replace to="/dashboard" />
  }

  return <>{children}</>
}
