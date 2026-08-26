import { useState, type FormEvent } from "react"
import {
  Activity,
  ArrowRight,
  Eye,
  EyeOff,
  KeyRound,
  LoaderCircle,
  Server,
  ShieldCheck,
  UserRound,
} from "lucide-react"
import { useIntl } from "react-intl"
import { useNavigate } from "react-router-dom"

import { DocumentTitle } from "@/app/document-title"
import { useAuthStore } from "@/auth/auth-store"
import { LocaleSwitcher } from "@/components/i18n/locale-switcher"
import { ThemeSwitcher } from "@/components/theme/theme-switcher"
import { Button } from "@/components/ui/button"
import { ManagerApiError } from "@/lib/manager-api"
import { defaultAppPath } from "@/lib/navigation"

function getLoginError(intl: ReturnType<typeof useIntl>, error: unknown) {
  if (error instanceof ManagerApiError) {
    if (error.status === 400) {
      return { hasCredentialError: true, message: intl.formatMessage({ id: "auth.invalidRequest" }) }
    }
    if (error.status === 401) {
      return { hasCredentialError: true, message: intl.formatMessage({ id: "auth.invalidCredentials" }) }
    }
    if (error.status >= 500) {
      return { hasCredentialError: false, message: intl.formatMessage({ id: "auth.serviceUnavailable" }) }
    }
  }

  return { hasCredentialError: false, message: intl.formatMessage({ id: "auth.unexpectedError" }) }
}

function BrandLockup({ inverted = false }: { inverted?: boolean }) {
  const intl = useIntl()

  return (
    <div className="flex items-center gap-3">
      <span
        className={`grid size-10 place-items-center rounded-xl border ${
          inverted ? "border-white/15 bg-white/10" : "border-border bg-card"
        }`}
      >
        <img alt="" className="h-6 w-auto" src="/favicon.svg" />
      </span>
      <div>
        <div className={`text-sm font-semibold tracking-[-0.02em] ${inverted ? "text-white" : "text-foreground"}`}>
          {intl.formatMessage({ id: "auth.brand" })}
        </div>
        <div
          className={`mt-0.5 font-mono text-[10px] uppercase tracking-[0.18em] ${
            inverted ? "text-slate-400" : "text-muted-foreground"
          }`}
        >
          {intl.formatMessage({ id: "auth.brandSubtitle" })}
        </div>
      </div>
    </div>
  )
}

function ManagerPreview() {
  const intl = useIntl()

  const capabilities = [
    intl.formatMessage({ id: "auth.capability.hashSlots" }),
    intl.formatMessage({ id: "auth.capability.liveTelemetry" }),
    intl.formatMessage({ id: "auth.capability.permissionScoped" }),
  ]

  return (
    <div className="relative mt-auto overflow-hidden rounded-[22px] border border-white/10 bg-white/[0.055] p-5">
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="font-mono text-[10px] uppercase tracking-[0.2em] text-slate-400">
            {intl.formatMessage({ id: "auth.preview.eyebrow" })}
          </div>
          <div className="mt-2 text-lg font-medium text-white">
            {intl.formatMessage({ id: "auth.preview.title" })}
          </div>
        </div>
        <div className="flex items-center gap-2 rounded-full border border-emerald-400/20 bg-emerald-400/10 px-3 py-1.5 text-xs text-emerald-200">
          <span className="size-1.5 rounded-full bg-emerald-300" />
          {intl.formatMessage({ id: "auth.preview.ready" })}
        </div>
      </div>

      <div aria-hidden className="relative my-4 h-24 rounded-2xl border border-white/10 bg-[#091f32] min-[1400px]:h-28">
        <div className="absolute left-[20%] right-[20%] top-1/2 h-px bg-white/15" />
        <div className="absolute bottom-[24%] left-1/2 top-[24%] w-px bg-white/15" />

        <div className="absolute left-[11%] top-1/2 flex -translate-y-1/2 items-center gap-2 rounded-xl border border-white/10 bg-[#0c2940] px-3 py-2.5">
          <Server className="size-4 text-sky-300" />
          <span className="font-mono text-[10px] text-slate-300">
            {intl.formatMessage({ id: "auth.preview.node" })}
          </span>
        </div>
        <div className="absolute left-1/2 top-1/2 grid size-11 -translate-x-1/2 -translate-y-1/2 place-items-center rounded-full border border-emerald-300/35 bg-emerald-300/10">
          <Activity className="size-5 text-emerald-300" />
        </div>
        <div className="absolute right-[11%] top-1/2 flex -translate-y-1/2 items-center gap-2 rounded-xl border border-white/10 bg-[#0c2940] px-3 py-2.5">
          <KeyRound className="size-4 text-violet-300" />
          <span className="font-mono text-[10px] text-slate-300">
            {intl.formatMessage({ id: "auth.preview.manager" })}
          </span>
        </div>
      </div>

      <div className="grid grid-cols-3 divide-x divide-white/10 border-t border-white/10 pt-4">
        {capabilities.map((capability) => (
          <div className="px-3 first:pl-0 last:pr-0" key={capability}>
            <div className="text-xs leading-5 text-slate-300">{capability}</div>
          </div>
        ))}
      </div>
    </div>
  )
}

export function LoginPage() {
  const intl = useIntl()
  const login = useAuthStore((state) => state.login)
  const navigate = useNavigate()
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [isPasswordVisible, setIsPasswordVisible] = useState(false)
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [errorMessage, setErrorMessage] = useState("")
  const [hasCredentialError, setHasCredentialError] = useState(false)

  function clearError() {
    setErrorMessage("")
    setHasCredentialError(false)
  }

  async function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setIsSubmitting(true)
    clearError()

    try {
      await login({ username, password })
      navigate(defaultAppPath, { replace: true })
    } catch (error) {
      const loginError = getLoginError(intl, error)
      setErrorMessage(loginError.message)
      setHasCredentialError(loginError.hasCredentialError)
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <main className="min-h-svh bg-[#eeece7] p-2.5 transition-colors dark:bg-[#04111d] sm:p-4 lg:p-3">
      <DocumentTitle titleMessageId="auth.signIn" />
      <div className="mx-auto grid min-h-[calc(100svh-1.25rem)] w-full max-w-[1440px] overflow-hidden rounded-[28px] border border-black/8 bg-card dark:border-white/10 sm:min-h-[calc(100svh-2rem)] lg:min-h-[calc(100svh-1.5rem)] lg:grid-cols-[minmax(0,1.1fr)_minmax(440px,0.9fr)]">
        <section
          className="relative hidden min-h-[680px] overflow-hidden bg-[#071829] px-10 py-8 text-white lg:flex lg:flex-col xl:px-14"
          data-testid="login-manager-preview"
        >
          <div aria-hidden className="absolute -right-24 top-20 size-72 rounded-full border border-white/[0.06]" />
          <div aria-hidden className="absolute -right-8 top-36 size-44 rounded-full border border-white/[0.06]" />

          <BrandLockup inverted />

          <div className="relative mt-12 max-w-xl xl:mt-16">
            <div className="font-mono text-[11px] uppercase tracking-[0.22em] text-sky-300">
              {intl.formatMessage({ id: "auth.operationsCockpit" })}
            </div>
            <h2 className="mt-4 text-5xl font-medium leading-[1.03] tracking-[-0.055em] text-white xl:text-[54px]">
              {intl.formatMessage({ id: "auth.heroTitle" })}
            </h2>
            <p className="mt-4 max-w-lg text-base leading-7 text-slate-300 xl:text-[17px]">
              {intl.formatMessage({ id: "auth.heroDescription" })}
            </p>
          </div>

          <ManagerPreview />
        </section>

        <section
          className="flex min-h-[calc(100svh-1.25rem)] flex-col bg-background px-5 py-5 text-foreground dark:bg-[#0b1f33] sm:min-h-[calc(100svh-2rem)] sm:px-10 sm:py-8 lg:min-h-[680px] lg:px-12 xl:px-16"
          data-testid="login-form-panel"
        >
          <header className="flex flex-col gap-4 sm:flex-row sm:items-center sm:justify-between lg:justify-end">
            <div className="lg:hidden">
              <BrandLockup />
              <div className="mt-4 max-w-sm">
                <div className="text-sm font-semibold tracking-[-0.02em] text-foreground">
                  {intl.formatMessage({ id: "auth.heroTitle" })}
                </div>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">
                  {intl.formatMessage({ id: "auth.mobileHeroDescription" })}
                </p>
              </div>
            </div>
            <div className="flex flex-wrap items-center gap-2">
              <ThemeSwitcher />
              <LocaleSwitcher />
            </div>
          </header>

          <div className="mx-auto flex w-full max-w-[430px] flex-1 flex-col justify-center py-6 lg:py-0">
            <div className="flex size-11 items-center justify-center rounded-2xl bg-accent text-accent-foreground">
              <ShieldCheck aria-hidden className="size-5" />
            </div>
            <div className="mt-6 font-mono text-[10px] font-medium uppercase tracking-[0.2em] text-muted-foreground">
              {intl.formatMessage({ id: "auth.secureAccess" })}
            </div>
            <h1 className="mt-3 text-4xl font-semibold tracking-[-0.045em] text-foreground sm:text-5xl">
              {intl.formatMessage({ id: "auth.signIn" })}
            </h1>
            <p className="mt-4 text-sm leading-6 text-muted-foreground">
              {intl.formatMessage({ id: "auth.staticAccountHint" })}
            </p>

            <form aria-busy={isSubmitting} className="mt-9 space-y-5" onSubmit={handleSubmit}>
              <div>
                <label className="text-sm font-medium text-foreground" htmlFor="manager-username">
                  {intl.formatMessage({ id: "auth.username" })}
                </label>
                <div className="relative mt-2">
                  <UserRound aria-hidden className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                  <input
                    aria-describedby={hasCredentialError ? "login-error" : undefined}
                    aria-invalid={hasCredentialError}
                    autoCapitalize="none"
                    autoComplete="username"
                    autoFocus
                    className="h-12 w-full rounded-xl border border-input bg-background pl-11 pr-4 text-sm text-foreground outline-none transition placeholder:text-muted-foreground/70 hover:border-foreground/30 focus:border-ring focus:ring-3 focus:ring-ring/15 disabled:cursor-not-allowed disabled:opacity-60 dark:bg-[#071829]"
                    disabled={isSubmitting}
                    id="manager-username"
                    name="username"
                    onChange={(event) => {
                      setUsername(event.target.value)
                      clearError()
                    }}
                    spellCheck={false}
                    type="text"
                    value={username}
                  />
                </div>
              </div>

              <div>
                <label className="text-sm font-medium text-foreground" htmlFor="manager-password">
                  {intl.formatMessage({ id: "auth.password" })}
                </label>
                <div className="relative mt-2">
                  <KeyRound aria-hidden className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
                  <input
                    aria-describedby={hasCredentialError ? "login-error" : undefined}
                    aria-invalid={hasCredentialError}
                    autoComplete="current-password"
                    className="h-12 w-full rounded-xl border border-input bg-background pl-11 pr-12 text-sm text-foreground outline-none transition hover:border-foreground/30 focus:border-ring focus:ring-3 focus:ring-ring/15 disabled:cursor-not-allowed disabled:opacity-60 dark:bg-[#071829]"
                    disabled={isSubmitting}
                    id="manager-password"
                    name="password"
                    onChange={(event) => {
                      setPassword(event.target.value)
                      clearError()
                    }}
                    type={isPasswordVisible ? "text" : "password"}
                    value={password}
                  />
                  <button
                    aria-label={intl.formatMessage({
                      id: isPasswordVisible ? "auth.hidePassword" : "auth.showPassword",
                    })}
                    className="absolute right-2 top-1/2 grid size-8 -translate-y-1/2 place-items-center rounded-lg text-muted-foreground transition hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/35"
                    disabled={isSubmitting}
                    onClick={() => setIsPasswordVisible((visible) => !visible)}
                    type="button"
                  >
                    {isPasswordVisible ? <EyeOff aria-hidden className="size-4" /> : <Eye aria-hidden className="size-4" />}
                  </button>
                </div>
              </div>

              {errorMessage ? (
                <div
                  aria-live="polite"
                  className="flex items-start gap-2.5 rounded-xl border border-destructive/25 bg-destructive/8 px-3.5 py-3 text-sm leading-5 text-destructive"
                  id="login-error"
                  role="alert"
                >
                  <span aria-hidden className="mt-1 size-1.5 shrink-0 rounded-full bg-current" />
                  {errorMessage}
                </div>
              ) : null}

              <Button className="h-12 w-full text-sm" disabled={isSubmitting} size="lg" type="submit">
                {isSubmitting ? (
                  <>
                    <LoaderCircle aria-hidden className="animate-spin" />
                    {intl.formatMessage({ id: "auth.signingIn" })}
                  </>
                ) : (
                  <>
                    {intl.formatMessage({ id: "auth.signIn" })}
                    <ArrowRight aria-hidden data-icon="inline-end" />
                  </>
                )}
              </Button>
            </form>

            <div className="mt-6 flex items-start gap-2.5 border-t border-border pt-5 text-xs leading-5 text-muted-foreground">
              <ShieldCheck aria-hidden className="mt-0.5 size-3.5 shrink-0" />
              {intl.formatMessage({ id: "auth.permissionNote" })}
            </div>
          </div>

          <footer className="flex items-center justify-between gap-4 border-t border-border pt-5 text-[11px] text-muted-foreground">
            <span>WuKongIM</span>
            <span className="font-mono uppercase tracking-[0.12em]">
              {intl.formatMessage({ id: "auth.consoleLabel" })}
            </span>
          </footer>
        </section>
      </div>
    </main>
  )
}
