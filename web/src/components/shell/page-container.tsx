import type { PropsWithChildren } from "react"

import { cn } from "@/lib/utils"

type PageContainerProps = PropsWithChildren<{
  className?: string
}>

export function PageContainer({ children, className }: PageContainerProps) {
  return (
    <div className={cn("mx-auto flex w-full max-w-[1440px] flex-col gap-4 px-5 py-5", className)}>
      {children}
    </div>
  )
}
