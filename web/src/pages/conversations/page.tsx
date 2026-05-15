import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"

export function ConversationsPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        title={intl.formatMessage({ id: "conversations.title" })}
        description={intl.formatMessage({ id: "conversations.description" })}
      />
      <p className="text-sm text-muted-foreground">
        {intl.formatMessage({ id: "conversations.summary.scopePending" })}
      </p>
    </PageContainer>
  )
}
