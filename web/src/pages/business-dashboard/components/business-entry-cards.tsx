import { useIntl } from "react-intl"
import { Link } from "react-router-dom"

import { SectionCard } from "@/components/shell/section-card"
import type { BusinessEntryCard } from "../view-model"

type BusinessEntryCardsProps = {
  cards: BusinessEntryCard[]
}

export function BusinessEntryCards({ cards }: BusinessEntryCardsProps) {
  const intl = useIntl()
  return (
    <SectionCard title={intl.formatMessage({ id: "businessDashboard.entries.title" })}>
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-5">
        {cards.map((card) => (
          <Link className="rounded-2xl border border-border/80 bg-muted/20 px-4 py-4 transition-colors hover:border-primary/40" key={card.key} to={card.href}>
            <div className="flex items-center justify-between gap-2">
              <span className="text-sm font-semibold text-foreground">{intl.formatMessage({ id: card.titleId })}</span>
              {card.source === "sample" ? (
                <span className="rounded-full border border-border bg-background/70 px-2 py-0.5 text-[10px] text-muted-foreground">
                  {intl.formatMessage({ id: "businessDashboard.source.sample" })}
                </span>
              ) : null}
            </div>
            <div className="mt-2 font-mono text-2xl font-semibold text-foreground">{card.value}</div>
            <p className="mt-2 text-xs leading-5 text-muted-foreground">{intl.formatMessage({ id: card.descriptionId })}</p>
          </Link>
        ))}
      </div>
    </SectionCard>
  )
}
