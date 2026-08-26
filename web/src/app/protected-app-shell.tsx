import { AppShell } from "@/app/layout/app-shell"
import { ProtectedRoute } from "@/auth/protected-route"

export function ProtectedAppShell() {
  return (
    <ProtectedRoute>
      <AppShell />
    </ProtectedRoute>
  )
}
