import { useIntl } from "react-intl"

import { PageContainer } from "@/components/shell/page-container"
import { PageHeader } from "@/components/shell/page-header"

export function WorkqueuesPage() {
  const intl = useIntl()
  return (
    <PageContainer>
      <PageHeader
        description={intl.formatMessage({ id: "workqueues.description" })}
        title={intl.formatMessage({ id: "workqueues.title" })}
      />
    </PageContainer>
  )
}
