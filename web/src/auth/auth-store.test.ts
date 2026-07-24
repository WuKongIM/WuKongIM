import { beforeEach, describe, expect, it } from "vitest"

import {
  AUTH_STORAGE_KEY,
  createAnonymousAuthState,
  useAuthStore,
} from "@/auth/auth-store"

describe("auth store session lifecycle", () => {
  beforeEach(() => {
    localStorage.clear()
    useAuthStore.setState(createAnonymousAuthState())
  })

  it("restores a persisted unexpired session", () => {
    localStorage.setItem(
      AUTH_STORAGE_KEY,
      JSON.stringify({
        username: "admin",
        tokenType: "Bearer",
        accessToken: "token-1",
        expiresAt: "2099-04-22T12:00:00Z",
        permissions: [{ resource: "cluster.node", actions: ["r"] }],
      }),
    )

    useAuthStore.getState().restoreSession()

    expect(useAuthStore.getState().status).toBe("authenticated")
    expect(useAuthStore.getState().username).toBe("admin")
    expect(useAuthStore.getState().isHydrated).toBe(true)
  })

  it("drops an expired persisted session during restore", () => {
    localStorage.setItem(
      AUTH_STORAGE_KEY,
      JSON.stringify({
        username: "admin",
        tokenType: "Bearer",
        accessToken: "expired",
        expiresAt: "2000-01-01T00:00:00Z",
        permissions: [],
      }),
    )

    useAuthStore.getState().restoreSession()

    expect(useAuthStore.getState().status).toBe("anonymous")
    expect(useAuthStore.getState().isHydrated).toBe(true)
    expect(localStorage.getItem(AUTH_STORAGE_KEY)).toBeNull()
  })

  it("clears memory and storage on logout", () => {
    useAuthStore.setState({
      status: "authenticated",
      isHydrated: true,
      username: "admin",
      tokenType: "Bearer",
      accessToken: "token-1",
      expiresAt: "2099-04-22T12:00:00Z",
      permissions: [],
    })
    localStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify({ username: "admin" }))

    useAuthStore.getState().logout()

    expect(useAuthStore.getState().status).toBe("anonymous")
    expect(useAuthStore.getState().username).toBe("")
    expect(localStorage.getItem(AUTH_STORAGE_KEY)).toBeNull()
  })

  it("resets the session when unauthorized handling runs", () => {
    useAuthStore.setState({
      status: "authenticated",
      isHydrated: true,
      username: "admin",
      tokenType: "Bearer",
      accessToken: "token-1",
      expiresAt: "2099-04-22T12:00:00Z",
      permissions: [],
    })
    localStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify({ username: "admin" }))

    useAuthStore.getState().handleUnauthorized()

    expect(useAuthStore.getState().status).toBe("anonymous")
    expect(useAuthStore.getState().isHydrated).toBe(true)
    expect(localStorage.getItem(AUTH_STORAGE_KEY)).toBeNull()
  })

  it("enters a non-persisted backup-only readonly session when manager authentication is disabled", () => {
    useAuthStore.getState().enterAuthDisabledReadonly()

    expect(useAuthStore.getState()).toMatchObject({
      status: "readonly",
      isHydrated: true,
      username: "read-only",
      accessToken: "",
      permissions: [{ resource: "cluster.backup", actions: ["r"] }],
    })
    expect(localStorage.getItem(AUTH_STORAGE_KEY)).toBeNull()
  })
})
