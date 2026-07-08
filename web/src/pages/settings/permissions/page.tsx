import { useCallback, useEffect, useState } from "react"
import { useIntl } from "react-intl"

import { ResourceState } from "@/components/manager/resource-state"
import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"
import { SectionCard } from "@/components/shell/section-card"
import { getPermissions, ManagerApiError } from "@/lib/manager-api"
import type {
  ManagerPermissionGrant,
  ManagerPermissionsResponse,
} from "@/lib/manager-api.types"

type PermissionsState = {
  snapshot: ManagerPermissionsResponse | null
  loading: boolean
  error: Error | null
}

function emptyPermissionsState(): PermissionsState {
  return {
    snapshot: null,
    loading: true,
    error: null,
  }
}

function mapErrorKind(error: Error | null) {
  if (!(error instanceof ManagerApiError)) {
    return "error" as const
  }
  if (error.status === 403) {
    return "forbidden" as const
  }
  if (error.status === 503) {
    return "unavailable" as const
  }
  return "error" as const
}

function grantLabel(grant: ManagerPermissionGrant) {
  return `${grant.resource}:${grant.actions.join("/")}`
}

function actionLabel(actions: string[]) {
  return actions.join(" / ")
}

export function PermissionsPage() {
  const intl = useIntl()
  const [state, setState] = useState<PermissionsState>(emptyPermissionsState)

  const runQuery = useCallback(async () => {
    setState((current) => ({ ...current, loading: true, error: null }))
    try {
      const snapshot = await getPermissions()
      setState({ snapshot, loading: false, error: null })
    } catch (error) {
      setState({
        snapshot: null,
        loading: false,
        error: error instanceof Error ? error : new Error("permissions request failed"),
      })
    }
  }, [])

  useEffect(() => {
    void runQuery()
  }, [runQuery])

  const snapshot = state.snapshot

  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "permissions.title" })}
        description={intl.formatMessage({ id: "permissions.description" })}
      />

      {state.loading ? <ResourceState kind="loading" title={intl.formatMessage({ id: "permissions.title" })} /> : null}
      {!state.loading && state.error ? (
        <ResourceState
          kind={mapErrorKind(state.error)}
          onRetry={() => {
            void runQuery()
          }}
          title={intl.formatMessage({ id: "permissions.title" })}
        />
      ) : null}

      {!state.loading && !state.error && snapshot ? (
        <>
          <SectionCard
            description={intl.formatMessage({ id: "permissions.summary.description" })}
            title={intl.formatMessage({ id: "permissions.summary.title" })}
          >
            <div
              className="grid overflow-hidden rounded-md border border-border bg-card md:grid-cols-4"
              data-testid="permissions-summary-strip"
            >
              <div
                className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0"
                data-permission-summary-cell=""
              >
                <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.auth" })}</div>
                <div className="mt-1 font-semibold text-foreground">
                  {snapshot.auth_enabled
                    ? intl.formatMessage({ id: "permissions.auth.enabled" })
                    : intl.formatMessage({ id: "permissions.auth.disabled" })}
                </div>
              </div>
              <div
                className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0"
                data-permission-summary-cell=""
              >
                <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.currentUser" })}</div>
                <div className="mt-1 font-semibold text-foreground">
                  {snapshot.current_user
                    ? intl.formatMessage({ id: "permissions.currentUser" }, { user: snapshot.current_user })
                    : intl.formatMessage({ id: "permissions.currentUser.empty" })}
                </div>
              </div>
              <div
                className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0"
                data-permission-summary-cell=""
              >
                <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.staticUsers" })}</div>
                <div className="mt-1 font-semibold text-foreground">
                  {intl.formatMessage({ id: "permissions.staticUsers" }, { count: snapshot.users.length })}
                </div>
              </div>
              <div
                className="border-b border-border px-3 py-3 text-sm last:border-b-0 md:border-r md:border-b-0 md:last:border-r-0"
                data-permission-summary-cell=""
              >
                <div className="text-muted-foreground">{intl.formatMessage({ id: "permissions.summary.catalog" })}</div>
                <div className="mt-1 font-semibold text-foreground">
                  {intl.formatMessage({ id: "permissions.catalogResources" }, { count: snapshot.resources.length })}
                </div>
              </div>
            </div>
            <p
              className="mt-4 border-t border-border pt-3 text-sm text-muted-foreground"
              data-testid="permissions-readonly-notice"
            >
              {intl.formatMessage({ id: "permissions.readonlyNotice" })}
            </p>
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "permissions.users.description" })}
            title={intl.formatMessage({ id: "permissions.users.title" })}
          >
            {snapshot.users.length > 0 ? (
              <div
                className="overflow-x-auto rounded-md border border-border"
                data-permissions-surface="users"
              >
                <table
                  aria-label={intl.formatMessage({ id: "permissions.users.title" })}
                  className="w-full border-collapse text-sm"
                >
                  <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                    <tr>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "permissions.table.username" })}</th>
                      <th className="px-3 py-3">{intl.formatMessage({ id: "permissions.table.permissions" })}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {snapshot.users.map((user) => (
                      <tr className="border-t border-border" key={user.username}>
                        <td className="px-3 py-3 text-sm font-medium text-foreground">{user.username}</td>
                        <td className="px-3 py-3 text-sm">
                          <div className="flex flex-wrap gap-1">
                            {user.permissions.map((grant) => (
                              <span
                                className="rounded-full border border-border bg-muted/40 px-2 py-1 text-xs font-medium text-foreground"
                                key={grantLabel(grant)}
                              >
                                {grantLabel(grant)}
                              </span>
                            ))}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <ResourceState
                description={intl.formatMessage({ id: "permissions.users.empty" })}
                kind="empty"
                title={intl.formatMessage({ id: "permissions.users.title" })}
              />
            )}
          </SectionCard>

          <SectionCard
            description={intl.formatMessage({ id: "permissions.catalog.description" })}
            title={intl.formatMessage({ id: "permissions.catalog.title" })}
          >
            <div
              className="overflow-x-auto rounded-md border border-border"
              data-permissions-surface="catalog"
            >
              <table
                aria-label={intl.formatMessage({ id: "permissions.catalog.title" })}
                className="w-full border-collapse text-sm"
              >
                <thead className="bg-muted/40 text-left text-xs uppercase tracking-[0.14em] text-muted-foreground">
                  <tr>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "permissions.table.resource" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "permissions.table.actions" })}</th>
                    <th className="px-3 py-3">{intl.formatMessage({ id: "permissions.table.description" })}</th>
                  </tr>
                </thead>
                <tbody>
                  {snapshot.resources.map((resource) => (
                    <tr className="border-t border-border" key={resource.resource}>
                      <td className="px-3 py-3 text-sm font-medium text-foreground">{resource.resource}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{actionLabel(resource.actions)}</td>
                      <td className="px-3 py-3 text-sm text-muted-foreground">{resource.description}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </SectionCard>
        </>
      ) : null}
    </PageContainer>
  )
}
