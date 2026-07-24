import type { AuthPermission } from "@/auth/auth-store"

export function hasManagerPermission(
  permissions: AuthPermission[],
  resource: string,
  action: string,
) {
  return permissions.some((permission) => {
    if (permission.resource !== resource && permission.resource !== "*") {
      return false
    }
    return permission.actions.includes(action) || permission.actions.includes("*")
  })
}
