import type { PropsWithChildren } from "react"

import { cn } from "@/lib/utils"

type PageContainerProps = PropsWithChildren<{
  className?: string
}>

export function PageContainer({ children, className }: PageContainerProps) {
  return (
    <div className={cn("mx-auto flex w-full max-w-[1560px] flex-col gap-4 px-4 py-5 sm:px-5 lg:px-7", className)}>
      {children}
    </div>
  )
}
