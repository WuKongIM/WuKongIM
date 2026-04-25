# Web Manager Login And Auth Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a real manager login flow to `web/` with a dedicated `/login` page, persisted JWT session state, protected routes, topbar logout, and authenticated requests to `POST /manager/login` and future `/manager/*` APIs.

**Architecture:** Keep authentication in a focused frontend auth slice built around a single `Zustand` store plus `localStorage` persistence. Keep the HTTP client store-agnostic by configuring token/unauthorized callbacks from `AppProviders`, then wrap the existing `AppShell` routes with a small `ProtectedRoute` and add a standalone login page.

**Tech Stack:** React 19, React Router 7, TypeScript, Vite, Vitest, Testing Library, `Zustand`, browser `fetch`, `localStorage`, existing `web/` shell components.

---

## References

- Spec: `docs/superpowers/specs/2026-04-22-web-manager-login-auth-design.md`
- Follow `@superpowers:test-driven-development` for every code slice.
- Run `@superpowers:verification-before-completion` before claiming the implementation is done.
- `web/` currently has no `FLOW.md`; re-check before editing in case one appears.
- Keep the existing monochrome shell language from `docs/superpowers/specs/2026-04-22-web-admin-shell-monochrome-simplification-design.md`.

## Critical Review Notes Before Starting

- The spec text says `manager-api` can call `authStore.getState().handleUnauthorized()`, but that creates a circular import if the store also calls `loginManager()`. Implement the same behavior through a tiny client configuration hook instead: `configureManagerAuth({ getAccessToken, onUnauthorized })`, wired once from `AppProviders`.
- Route hydration must not flash `/login` before session restore completes. Treat `isHydrated` as part of the contract and cover it with tests before changing router structure.

## File Structure

- Modify: `web/package.json` — add `zustand` and keep scripts unchanged.
- Modify: `web/bun.lock` — lockfile update for the new dependency.
- Create: `web/src/auth/auth-store.ts` — auth session types, `Zustand` store, `localStorage` key/helpers, login/logout/restore actions.
- Create: `web/src/auth/auth-store.test.ts` — store-level tests for restore, expiry cleanup, logout, and unauthorized reset.
- Create: `web/src/auth/protected-route.tsx` — route guard that waits for hydration and redirects anonymous users.
- Create: `web/src/lib/env.ts` — `VITE_API_BASE_URL` normalization helper.
- Create: `web/src/lib/manager-api.ts` — login request plus generic authenticated request helper and auth callback registration.
- Create: `web/src/lib/manager-api.test.ts` — client tests for URL building, headers, login parsing, and `401` behavior.
- Modify: `web/src/app/providers.tsx` — bootstrap auth restore and register manager API auth callbacks.
- Modify: `web/src/app/router.tsx` — add `/login`, protected route wrapper, and logged-in redirect behavior.
- Modify: `web/src/app/router.test.tsx` — route guard coverage.
- Create: `web/src/pages/login/page.tsx` — login screen and submit/error UI.
- Create: `web/src/pages/login/page.test.tsx` — login page success/error/loading tests.
- Modify: `web/src/app/layout/topbar.tsx` — show username and `Logout` button.
- Modify: `web/src/app/layout/topbar.test.tsx` — topbar auth UI tests.
- Modify: `web/README.md` — update scope text and add `VITE_API_BASE_URL` note.

### Task 1: Add the auth store core and session persistence

**Files:**
- Modify: `web/package.json`
- Modify: `web/bun.lock`
- Create: `web/src/auth/auth-store.ts`
- Create: `web/src/auth/auth-store.test.ts`

- [ ] **Step 1: Write the failing auth store tests**

Add store-level tests that lock the session contract before adding any production auth logic, for example:

```ts
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
    expect(localStorage.getItem(AUTH_STORAGE_KEY)).toBeNull()
  })
})
```

Also add a focused `handleUnauthorized()` test so forced sign-out and manual logout stay equivalent.

- [ ] **Step 2: Run the focused auth store tests to verify they fail**

Run: `cd web && bun run test -- src/auth/auth-store.test.ts`
Expected: FAIL because the auth store, storage key, and restore/logout behavior do not exist yet.

- [ ] **Step 3: Implement the minimal auth store and install `zustand`**

Add the dependency and implement a focused store with a tiny state model and no page-specific UI state:

```ts
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

export const AUTH_STORAGE_KEY = "wukongim_manager_auth"

export const createAnonymousAuthState = () => ({
  status: "anonymous" as const,
  isHydrated: false,
  username: "",
  tokenType: "",
  accessToken: "",
  expiresAt: "",
  permissions: [],
})
```

Keep `restoreSession()`, `logout()`, and `handleUnauthorized()` synchronous for now. Defer the real network `login()` action to a later task so this slice stays small and testable.

- [ ] **Step 4: Re-run the focused auth store tests to verify they pass**

Run: `cd web && bun run test -- src/auth/auth-store.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit the auth store core**

Run:

```bash
git add web/package.json web/bun.lock web/src/auth/auth-store.ts web/src/auth/auth-store.test.ts
git commit -m "feat: add web auth session store"
```

### Task 2: Add the manager API client and auth callback bridge

**Files:**
- Create: `web/src/lib/env.ts`
- Create: `web/src/lib/manager-api.ts`
- Create: `web/src/lib/manager-api.test.ts`

- [ ] **Step 1: Write the failing manager API tests**

Add client tests that lock URL, header, and `401` behavior before creating the client, for example:

```ts
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  configureManagerAuth,
  loginManager,
  managerFetch,
  resetManagerAuthConfig,
} from "@/lib/manager-api"

describe("manager api client", () => {
  const fetchMock = vi.fn()

  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock)
    resetManagerAuthConfig()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.unstubAllEnvs()
  })

  it("prefixes requests with VITE_API_BASE_URL and trims trailing slashes", async () => {
    vi.stubEnv("VITE_API_BASE_URL", "http://127.0.0.1:5301/")
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }))

    await managerFetch("/manager/nodes")

    expect(fetchMock).toHaveBeenCalledWith(
      "http://127.0.0.1:5301/manager/nodes",
      expect.any(Object),
    )
  })

  it("adds the bearer token when auth is configured", async () => {
    configureManagerAuth({
      getAccessToken: () => "token-1",
      onUnauthorized: vi.fn(),
    })
    fetchMock.mockResolvedValue(new Response("{}", { status: 200 }))

    await managerFetch("/manager/nodes")

    expect(fetchMock.mock.calls[0][1]?.headers).toMatchObject({
      Authorization: "Bearer token-1",
    })
  })

  it("calls onUnauthorized on 401 responses", async () => {
    const onUnauthorized = vi.fn()
    configureManagerAuth({ getAccessToken: () => "token-1", onUnauthorized })
    fetchMock.mockResolvedValue(new Response('{"error":"unauthorized"}', { status: 401 }))

    await expect(managerFetch("/manager/nodes")).rejects.toThrow()
    expect(onUnauthorized).toHaveBeenCalledTimes(1)
  })
})
```

Also add a login contract test that checks `loginManager()` returns the current backend fields and does not look for any legacy `token` key.

- [ ] **Step 2: Run the focused manager API tests to verify they fail**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`
Expected: FAIL because the client, env helper, and auth callback bridge do not exist yet.

- [ ] **Step 3: Implement the minimal client and callback registration**

Build a store-agnostic manager client with module-scoped auth hooks:

```ts
type ManagerAuthConfig = {
  getAccessToken: () => string
  onUnauthorized: () => void
}

let authConfig: ManagerAuthConfig | null = null

export function configureManagerAuth(config: ManagerAuthConfig) {
  authConfig = config
}

export async function managerFetch(path: string, init?: RequestInit) {
  const headers = new Headers(init?.headers)
  headers.set("Accept", "application/json")

  const token = authConfig?.getAccessToken()?.trim()
  if (token) {
    headers.set("Authorization", `Bearer ${token}`)
  }

  const response = await fetch(buildManagerUrl(path), { ...init, headers })
  if (response.status === 401) {
    authConfig?.onUnauthorized()
  }
  return response
}
```

Keep the client generic enough for future `/manager/*` reads, but only implement the login helper and the minimal response parsing required by this plan.

- [ ] **Step 4: Re-run the focused manager API tests to verify they pass**

Run: `cd web && bun run test -- src/lib/manager-api.test.ts`
Expected: PASS.

- [ ] **Step 5: Commit the client slice**

Run:

```bash
git add web/src/lib/env.ts web/src/lib/manager-api.ts web/src/lib/manager-api.test.ts
git commit -m "feat: add web manager api client"
```

### Task 3: Protect the existing routes and bootstrap auth hydration

**Files:**
- Create: `web/src/auth/protected-route.tsx`
- Modify: `web/src/app/providers.tsx`
- Modify: `web/src/app/router.tsx`
- Modify: `web/src/app/router.test.tsx`

- [ ] **Step 1: Write the failing router/auth guard tests**

Replace the current router assertions with auth-aware coverage, for example:

```tsx
import { render, screen } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test } from "vitest"

import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { AppProviders } from "@/app/providers"
import { routes } from "@/app/router"

beforeEach(() => {
  localStorage.clear()
  useAuthStore.setState({ ...createAnonymousAuthState(), isHydrated: true })
})

test("redirects anonymous /dashboard visits to /login", async () => {
  const router = createMemoryRouter(routes, { initialEntries: ["/dashboard"] })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: /sign in/i })).toBeInTheDocument()
})

test("redirects authenticated /login visits to /dashboard", async () => {
  useAuthStore.setState({
    status: "authenticated",
    isHydrated: true,
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
})
```

Also add one test that keeps `isHydrated=false` and confirms the login page is not shown immediately.

- [ ] **Step 2: Run the focused router tests to verify they fail**

Run: `cd web && bun run test -- src/app/router.test.tsx`
Expected: FAIL because `/login`, `ProtectedRoute`, and auth bootstrapping do not exist yet.

- [ ] **Step 3: Implement the route split and provider bootstrap**

Add a provider-side bootstrap effect that restores session state and registers the client callbacks once:

```tsx
export function AppProviders({ children }: AppProvidersProps) {
  const restoreSession = useAuthStore((state) => state.restoreSession)

  useEffect(() => {
    configureManagerAuth({
      getAccessToken: () => useAuthStore.getState().accessToken,
      onUnauthorized: () => useAuthStore.getState().handleUnauthorized(),
    })
    restoreSession()
  }, [restoreSession])

  return <TooltipProvider>{children}</TooltipProvider>
}
```

Then split the router so `/login` is public and all existing shell pages sit under a protected wrapper.

- [ ] **Step 4: Re-run the focused router tests to verify they pass**

Run: `cd web && bun run test -- src/app/router.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the route/auth bootstrap slice**

Run:

```bash
git add web/src/auth/protected-route.tsx web/src/app/providers.tsx web/src/app/router.tsx web/src/app/router.test.tsx
git commit -m "feat: protect web manager routes"
```

### Task 4: Add the login page and real sign-in flow

**Files:**
- Modify: `web/src/auth/auth-store.ts`
- Create: `web/src/pages/login/page.tsx`
- Create: `web/src/pages/login/page.test.tsx`

- [ ] **Step 1: Write the failing login page tests**

Add page tests that cover the actual UI contract before writing the page, for example:

```tsx
import userEvent from "@testing-library/user-event"
import { render, screen, waitFor } from "@testing-library/react"
import { RouterProvider, createMemoryRouter } from "react-router-dom"
import { beforeEach, expect, test, vi } from "vitest"

import { AppProviders } from "@/app/providers"
import { createAnonymousAuthState, useAuthStore } from "@/auth/auth-store"
import { routes } from "@/app/router"

const loginManagerMock = vi.fn()
vi.mock("@/lib/manager-api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@/lib/manager-api")>()
  return {
    ...actual,
    loginManager: (...args: unknown[]) => loginManagerMock(...args),
  }
})

beforeEach(() => {
  localStorage.clear()
  useAuthStore.setState({ ...createAnonymousAuthState(), isHydrated: true })
  loginManagerMock.mockReset()
})

test("submits credentials and redirects to /dashboard on success", async () => {
  loginManagerMock.mockResolvedValue({
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  const router = createMemoryRouter(routes, { initialEntries: ["/login"] })
  const user = userEvent.setup()

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  await user.type(screen.getByLabelText(/username/i), "admin")
  await user.type(screen.getByLabelText(/password/i), "secret")
  await user.click(screen.getByRole("button", { name: /sign in/i }))

  expect(await screen.findByRole("heading", { name: "Dashboard" })).toBeInTheDocument()
  expect(useAuthStore.getState().accessToken).toBe("token-1")
})
```

Also add tests for `401` error text and the `Signing in...` loading/disabled button state.

- [ ] **Step 2: Run the focused login page tests to verify they fail**

Run: `cd web && bun run test -- src/pages/login/page.test.tsx`
Expected: FAIL because the login page and async store login action do not exist yet.

- [ ] **Step 3: Implement the minimal sign-in action and page UI**

Extend the store with an async `login()` action that delegates to `loginManager()` and persists the returned session:

```ts
async login(credentials: { username: string; password: string }) {
  const session = await loginManager(credentials)
  set({
    status: "authenticated",
    isHydrated: true,
    username: session.username,
    tokenType: session.tokenType,
    accessToken: session.accessToken,
    expiresAt: session.expiresAt,
    permissions: session.permissions,
  })
  persistSession(session)
}
```

Then add a dedicated `/login` page that:
- uses the existing monochrome card/button language,
- renders a concise left-side product description plus right-side form,
- surfaces mapped errors for `400`, `401`, and service-unavailable responses,
- navigates to `/dashboard` after a successful submit.

- [ ] **Step 4: Re-run the focused login page tests to verify they pass**

Run: `cd web && bun run test -- src/pages/login/page.test.tsx`
Expected: PASS.

- [ ] **Step 5: Commit the login page slice**

Run:

```bash
git add web/src/auth/auth-store.ts web/src/pages/login/page.tsx web/src/pages/login/page.test.tsx
git commit -m "feat: add web manager login page"
```

### Task 5: Add topbar logout UI, then update docs and run full verification

**Files:**
- Modify: `web/src/app/layout/topbar.tsx`
- Modify: `web/src/app/layout/topbar.test.tsx`
- Modify: `web/README.md`

- [ ] **Step 1: Write the failing topbar auth tests**

Extend the topbar tests so they cover the authenticated chrome, for example:

```tsx
test("shows the logged-in username and a logout action", async () => {
  useAuthStore.setState({
    status: "authenticated",
    isHydrated: true,
    username: "admin",
    tokenType: "Bearer",
    accessToken: "token-1",
    expiresAt: "2099-04-22T12:00:00Z",
    permissions: [],
  })

  const router = createMemoryRouter(routes, { initialEntries: ["/network"] })
  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  expect(await screen.findByText("admin")).toBeInTheDocument()
  expect(screen.getByRole("button", { name: /logout/i })).toBeInTheDocument()
})
```

Also add one test that clicks `Logout` and confirms the router returns to `/login` and the auth store resets.

- [ ] **Step 2: Run the focused topbar tests to verify they fail**

Run: `cd web && bun run test -- src/app/layout/topbar.test.tsx`
Expected: FAIL because the topbar does not yet render auth state or logout behavior.

- [ ] **Step 3: Implement the topbar logout UI and update `web/README.md`**

Update the topbar without disturbing its monochrome shell tone:

```tsx
<div className="flex items-center gap-2">
  <span className="text-xs text-muted-foreground">{username}</span>
  <Button size="sm" variant="outline" onClick={logout}>
    Logout
  </Button>
</div>
```

Then update `web/README.md` so the scope and setup notes no longer say auth/API integration are missing. Mention that:
- `/login` authenticates against `POST /manager/login`,
- `VITE_API_BASE_URL` optionally overrides the default same-origin base,
- protected routes require a valid JWT session.

- [ ] **Step 4: Run the focused topbar tests, then the full web verification suite**

Run:

```bash
cd web && bun run test -- src/app/layout/topbar.test.tsx
cd web && bun run test
cd web && bun run build
```

Expected: all targeted tests PASS, the full `web` test suite PASSes, and the production build succeeds.

- [ ] **Step 5: Commit the topbar/docs slice**

Run:

```bash
git add web/src/app/layout/topbar.tsx web/src/app/layout/topbar.test.tsx web/README.md
git commit -m "feat: finish web manager auth flow"
```

## Local Plan Review Checklist

Before executing, confirm the written plan still satisfies the spec:

- [ ] `/login` is public and the existing shell routes are protected.
- [ ] Session restore is covered before router changes land.
- [ ] `401` handling does not rely on a circular store/client import.
- [ ] Topbar logout and login-page redirect behavior are both covered by tests.
- [ ] The full `web` test suite and build are part of final verification.

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-22-web-manager-login-auth.md`. Ready to execute?
