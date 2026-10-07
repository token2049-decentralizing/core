import { cn } from "@/lib/utils"

export function PageHeader({
  title,
  description,
  actions,
  className,
}: {
  title: React.ReactNode
  description?: React.ReactNode
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between",
        className
      )}
    >
      <div className="flex min-w-0 flex-col gap-1.5">
        <h1 className="font-heading text-2xl leading-tight font-bold tracking-tight text-balance break-words">
          {title}
        </h1>
        {description && (
          <div className="max-w-prose text-xs leading-relaxed text-muted-foreground">
            {description}
          </div>
        )}
      </div>
      {actions && (
        <div className="flex flex-wrap items-center gap-2 sm:shrink-0">
          {actions}
        </div>
      )}
    </div>
  )
}

export function PageBody({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "mx-auto flex w-full max-w-6xl flex-col gap-8 px-4 py-6 md:px-8 md:py-8",
        className
      )}
      {...props}
    />
  )
}

export function Section({
  title,
  actions,
  children,
  className,
}: {
  title: React.ReactNode
  actions?: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <section className={cn("flex flex-col gap-3", className)}>
      <div className="flex items-center justify-between gap-4">
        <h2 className="text-sm font-semibold">{title}</h2>
        {actions}
      </div>
      {children}
    </section>
  )
}
