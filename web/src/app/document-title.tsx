import { useEffect } from "react"
import { useIntl } from "react-intl"

const managerTitle = "WuKongIM Manager"

export function DocumentTitle({ titleMessageId }: { titleMessageId?: string }) {
  const intl = useIntl()
  const pageTitle = titleMessageId
    ? intl.formatMessage({ id: titleMessageId })
    : ""

  useEffect(() => {
    document.title = pageTitle ? `${pageTitle} · ${managerTitle}` : managerTitle
  }, [pageTitle])

  return null
}
