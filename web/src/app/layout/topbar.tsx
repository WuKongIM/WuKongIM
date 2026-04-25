import { Search } from "lucide-react"
import { useIntl } from "react-intl"
import { useLocation } from "react-router-dom"

import { useAuthStore } from "@/auth/auth-store"
import { LocaleSwitcher } from "@/components/i18n/locale-switcher"
import { Button } from "@/components/ui/button"
import { pageMetadata } from "@/lib/navigation"

export function Topbar() {
  const intl = useIntl()
  const location = useLocation()
  const page = pageMetadata.get(location.pathname) ?? pageMetadata.get("/dashboard")
  const username = useAuthStore((state) => state.username)
  const logout = useAuthStore((state) => state.logout)

  return (
    <header className="border-b border-border bg-background px-6 py-3" role="banner">
      <div className="flex items-center justify-between gap-4">
        <div className="min-w-0">
          <div className="text-sm font-semibold text-foreground">
            {page ? intl.formatMessage({ id: page.titleMessageId }) : null}
          </div>
          <p className="text-xs text-muted-foreground">
            {page ? intl.formatMessage({ id: page.descriptionMessageId }) : null}
          </p>
        </div>
        <div className="flex items-center gap-3">
          <div className="flex items-center gap-2">
            <LocaleSwitcher />
            <Button size="sm" variant="outline">
              {intl.formatMessage({ id: "common.refresh" })}
            </Button>
            <Button size="sm" variant="outline">
              <Search className="size-3.5" />
              {intl.formatMessage({ id: "common.search" })}
            </Button>
          </div>
          <div className="flex items-center gap-2 border-l border-border pl-3">
            <span className="text-xs text-muted-foreground">{username}</span>
            <Button onClick={logout} size="sm" variant="outline">
              {intl.formatMessage({ id: "common.logout" })}
            </Button>
          </div>
        </div>
      </div>
    </header>
  )
}
