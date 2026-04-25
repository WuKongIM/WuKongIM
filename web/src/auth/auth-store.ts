import { create } from "zustand"

import {
  loginManager,
  type ManagerLoginCredentials,
  type ManagerSession,
} from "@/lib/manager-api"

export type AuthStatus = "anonymous" | "authenticated"

export type AuthPermission = {
  resource: string
  actions: string[]
}

export type AuthSession = {
  username: string
  tokenType: string
  accessToken: string
  expiresAt: string
  permissions: AuthPermission[]
}

type AuthState = {
  status: AuthStatus
  isHydrated: boolean
  username: string
  tokenType: string
  accessToken: string
  expiresAt: string
  permissions: AuthPermission[]
  login: (credentials: ManagerLoginCredentials) => Promise<void>
  restoreSession: () => void
  logout: () => void
  handleUnauthorized: () => void
}

// AUTH_STORAGE_KEY keeps the persisted manager session in browser storage.
export const AUTH_STORAGE_KEY = "wukongim_manager_auth"

export function createAnonymousAuthState() {
  return {
    status: "anonymous" as const,
    isHydrated: false,
    username: "",
    tokenType: "",
    accessToken: "",
    expiresAt: "",
    permissions: [] as AuthPermission[],
  }
}

function createHydratedAnonymousState() {
  return {
    ...createAnonymousAuthState(),
    isHydrated: true,
  }
}

function persistSession(session: AuthSession) {
  localStorage.setItem(AUTH_STORAGE_KEY, JSON.stringify(session))
}

function clearPersistedSession() {
  localStorage.removeItem(AUTH_STORAGE_KEY)
}

function readPersistedSession(): AuthSession | null {
  const raw = localStorage.getItem(AUTH_STORAGE_KEY)
  if (!raw) {
    return null
  }

  try {
    const parsed = JSON.parse(raw) as Partial<AuthSession>
    if (
      typeof parsed.username !== "string" ||
      typeof parsed.tokenType !== "string" ||
      typeof parsed.accessToken !== "string" ||
      typeof parsed.expiresAt !== "string" ||
      !Array.isArray(parsed.permissions)
    ) {
      clearPersistedSession()
      return null
    }

    return {
      username: parsed.username,
      tokenType: parsed.tokenType,
      accessToken: parsed.accessToken,
      expiresAt: parsed.expiresAt,
      permissions: parsed.permissions,
    }
  } catch {
    clearPersistedSession()
    return null
  }
}

function isSessionExpired(expiresAt: string) {
  const timestamp = Date.parse(expiresAt)
  return Number.isNaN(timestamp) || timestamp <= Date.now()
}

function applyAuthenticatedSession(session: AuthSession) {
  return {
    status: "authenticated" as const,
    isHydrated: true,
    username: session.username,
    tokenType: session.tokenType,
    accessToken: session.accessToken,
    expiresAt: session.expiresAt,
    permissions: session.permissions,
  }
}

function toAuthSession(session: ManagerSession): AuthSession {
  return {
    username: session.username,
    tokenType: session.tokenType,
    accessToken: session.accessToken,
    expiresAt: session.expiresAt,
    permissions: session.permissions,
  }
}

export const useAuthStore = create<AuthState>((set) => ({
  ...createAnonymousAuthState(),
  login: async (credentials) => {
    const session = toAuthSession(await loginManager(credentials))
    persistSession(session)
    set(applyAuthenticatedSession(session))
  },
  restoreSession: () => {
    const session = readPersistedSession()
    if (!session || isSessionExpired(session.expiresAt)) {
      clearPersistedSession()
      set(createHydratedAnonymousState())
      return
    }

    persistSession(session)
    set(applyAuthenticatedSession(session))
  },
  logout: () => {
    clearPersistedSession()
    set(createHydratedAnonymousState())
  },
  handleUnauthorized: () => {
    clearPersistedSession()
    set(createHydratedAnonymousState())
  },
}))
