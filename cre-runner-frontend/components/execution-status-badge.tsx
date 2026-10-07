import type { ExecutionStatus } from "@/lib/api"
import { cn } from "@/lib/utils"

const STYLE: Record<ExecutionStatus, string> = {
  queued: "border-dashed border-border text-muted-foreground",
  running: "border-primary/30 bg-primary/10 text-primary",
  completed: "border-ev-push/40 bg-ev-push/10 text-foreground",
  failed: "border-destructive/40 bg-destructive/10 text-destructive",
  skipped: "border-border bg-muted text-muted-foreground",
}

const LABEL: Record<ExecutionStatus, string> = {
  queued: "Queued",
  running: "Running",
  completed: "Completed",
  failed: "Failed",
  skipped: "Skipped",
}

export function ExecutionStatusBadge({
  status,
  className,
}: {
  status: ExecutionStatus
  className?: string
}) {
  return (
    <span
      className={cn(
        "inline-flex h-5 shrink-0 items-center gap-1.5 border px-1.5 text-[11px] font-medium",
        STYLE[status],
        className
      )}
    >
      {status === "running" && (
        <span className="size-1.5 animate-pulse bg-primary" />
      )}
      {LABEL[status] ?? status}
    </span>
  )
}
