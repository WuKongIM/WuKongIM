import { ArrowRight, Bot, Hash, MessageSquareText, Users } from "lucide-react"
import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import type { BusinessEntryCard } from "../view-model"

type BusinessEntryCardsProps = {
  cards: BusinessEntryCard[]
}

const entryIcons = {
  users: Users,
  channels: Hash,
  messages: MessageSquareText,
  systemUsers: Bot,
} satisfies Record<BusinessEntryCard["key"], typeof Users>

export function BusinessEntryCards({ cards }: BusinessEntryCardsProps) {
  const intl = useIntl()

  return (
    <section className="rounded-lg border border-border/80 bg-card p-4">
      <div className="flex flex-col gap-1 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h2 className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: "businessDashboard.entries.title" })}</h2>
          <p className="text-xs leading-5 text-muted-foreground">{intl.formatMessage({ id: "businessDashboard.entries.description" })}</p>
        </div>
      </div>
      <div className="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-4">
        {cards.map((card) => (
          <EntryLink card={card} key={card.key} />
        ))}
      </div>
    </section>
  )
}

function EntryLink({ card }: { card: BusinessEntryCard }) {
  const intl = useIntl()
  const Icon = entryIcons[card.key]
  const title = intl.formatMessage({ id: card.titleId })

  return (
    <Link className="group rounded-lg border border-border/80 bg-background/55 p-3 transition-colors hover:border-primary/45 hover:bg-accent/35" to={card.href}>
      <div className="flex items-start justify-between gap-3">
        <span className="rounded-md border border-border bg-card p-2 text-muted-foreground transition-colors group-hover:text-primary">
          <Icon aria-hidden className="size-4" />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-sm font-semibold text-foreground">{title}</span>
              {card.source === "sample" ? (
              <span className="rounded-md border border-border bg-card px-1.5 py-0.5 text-[10px] text-muted-foreground">
                  {intl.formatMessage({ id: "businessDashboard.source.sample" })}
                </span>
              ) : null}
          </div>
          <div className="mt-2 font-mono text-xl font-semibold text-foreground">{card.value}</div>
          <p className="mt-2 min-h-10 text-xs leading-5 text-muted-foreground">{intl.formatMessage({ id: card.descriptionId })}</p>
        </div>
      </div>
      <span className="mt-3 inline-flex items-center gap-1 text-xs font-medium text-primary">
        {intl.formatMessage({ id: "businessDashboard.entries.open" }, { title })}
        <ArrowRight aria-hidden className="size-3.5 transition-transform group-hover:translate-x-0.5" />
      </span>
    </Link>
  )
}
