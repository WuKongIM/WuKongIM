import { useState, type FormEvent } from "react"
import { useIntl } from "react-intl"
import { useNavigate } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { LocaleSwitcher } from "@/components/i18n/locale-switcher"
import { Button } from "@/components/ui/button"
import { ManagerApiError } from "@/lib/manager-api"
import { defaultAppPath } from "@/lib/navigation"

function getLoginErrorMessage(intl: ReturnType<typeof useIntl>, error: unknown) {
  if (error instanceof ManagerApiError) {
    if (error.status === 400) {
      return intl.formatMessage({ id: "auth.invalidRequest" })
    }
    if (error.status === 401) {
      return intl.formatMessage({ id: "auth.invalidCredentials" })
    }
    if (error.status >= 500) {
      return intl.formatMessage({ id: "auth.serviceUnavailable" })
    }
  }

  return intl.formatMessage({ id: "auth.unexpectedError" })
}

export function LoginPage() {
  const intl = useIntl()
  const login = useAuthStore((state) => state.login)
  const navigate = useNavigate()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [errorMessage, setErrorMessage] = useState("")

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setIsSubmitting(true)
    setErrorMessage("")

    try {
      await login({ username, password })
      navigate(defaultAppPath, { replace: true })
    } catch (error) {
      setErrorMessage(getLoginErrorMessage(intl, error))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <main className="min-h-screen overflow-hidden bg-background px-5 py-8 sm:px-6 lg:py-12">
      <div className="relative mx-auto grid min-h-[calc(100vh-4rem)] w-full max-w-6xl items-center gap-8 lg:grid-cols-[1.08fr_0.92fr]">
        <section className="max-w-2xl">
          <div className="flex items-center justify-between gap-3">
            <div className="flex items-center gap-3">
              <div
                aria-hidden
                className="size-9 rounded-sm border border-foreground bg-foreground"
              />
              <div>
                <div className="font-mono text-[11px] font-semibold uppercase tracking-[0.28em] text-foreground">
                  {intl.formatMessage({ id: "auth.brand" })}
                </div>
                <div className="mt-1 text-[11px] font-medium uppercase tracking-[0.16em] text-muted-foreground">
                  {intl.formatMessage({ id: "auth.operationsCockpit" })}
                </div>
              </div>
            </div>
            <LocaleSwitcher />
          </div>
          <h1 className="mt-7 text-4xl font-semibold tracking-[-0.05em] text-foreground sm:text-6xl">
            {intl.formatMessage({ id: "auth.signIn" })}
          </h1>
          <p className="mt-4 max-w-xl text-sm leading-7 text-muted-foreground sm:text-base">
            {intl.formatMessage({ id: "auth.description" })}
          </p>
          <div className="mt-8 grid gap-3 text-sm text-muted-foreground sm:max-w-xl sm:grid-cols-3">
            <div className="rounded-2xl border border-border/80 bg-card/80 px-4 py-4">
              {intl.formatMessage({ id: "auth.feature.nodeInventory" })}
            </div>
            <div className="rounded-2xl border border-border/80 bg-card/80 px-4 py-4">
              {intl.formatMessage({ id: "auth.feature.slotCoordination" })}
            </div>
            <div className="rounded-2xl border border-border/80 bg-card/80 px-4 py-4">
              {intl.formatMessage({ id: "auth.feature.runtimeStatus" })}
            </div>
          </div>
          <div className="mt-4 flex flex-wrap gap-2 text-xs text-primary">
            <span className="rounded-full border border-primary/25 bg-primary/10 px-3 py-1.5">
              {intl.formatMessage({ id: "auth.singleNodeClusterReady" })}
            </span>
            <span className="rounded-full border border-primary/25 bg-primary/10 px-3 py-1.5">
              {intl.formatMessage({ id: "auth.healthFirstNavigation" })}
            </span>
          </div>
          <div
            className="mt-8 rounded-[22px] bg-[var(--primary)] p-6 text-primary-foreground"
            data-testid="login-brand-band"
          >
            <div className="font-mono text-[11px] uppercase tracking-[0.22em] opacity-80">
              {intl.formatMessage({ id: "auth.operationsCockpit" })}
            </div>
            <div className="mt-3 text-2xl font-normal leading-tight">
              {intl.formatMessage({ id: "auth.singleNodeClusterReady" })}
            </div>
          </div>
        </section>

        <section
          className="w-full rounded-[22px] border border-border bg-card p-6 text-card-foreground shadow-none sm:p-8"
          data-testid="login-form-card"
        >
          <div className="text-[11px] font-semibold uppercase tracking-[0.24em] text-muted-foreground">
            {intl.formatMessage({ id: "auth.clusterAccess" })}
          </div>
          <h2 className="mt-3 text-2xl font-semibold tracking-tight text-foreground">
            {intl.formatMessage({ id: "auth.managerCredentials" })}
          </h2>
          <p className="mt-2 text-sm text-muted-foreground">
            {intl.formatMessage({ id: "auth.staticAccountHint" })}
          </p>

          <form className="mt-8 space-y-5" onSubmit={handleSubmit}>
            <label className="block space-y-2">
              <span className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "auth.username" })}
              </span>
              <input
                autoComplete="username"
                className="w-full rounded-md border border-input bg-background px-3 py-2.5 text-sm text-foreground outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/30"
                name="username"
                onChange={(event) => setUsername(event.target.value)}
                type="text"
                value={username}
              />
            </label>

            <label className="block space-y-2">
              <span className="text-sm font-medium text-foreground">
                {intl.formatMessage({ id: "auth.password" })}
              </span>
              <input
                autoComplete="current-password"
                className="w-full rounded-md border border-input bg-background px-3 py-2.5 text-sm text-foreground outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/30"
                name="password"
                onChange={(event) => setPassword(event.target.value)}
                type="password"
                value={password}
              />
            </label>

            {errorMessage ? (
              <div
                aria-live="polite"
                className="rounded-md border border-destructive/25 bg-destructive/8 px-3 py-2 text-sm text-destructive"
                role="alert"
              >
                {errorMessage}
              </div>
            ) : null}

            <Button className="w-full" disabled={isSubmitting} size="lg" type="submit">
              {isSubmitting
                ? intl.formatMessage({ id: "auth.signingIn" })
                : intl.formatMessage({ id: "auth.signIn" })}
            </Button>
          </form>
        </section>
      </div>
    </main>
  )
}
