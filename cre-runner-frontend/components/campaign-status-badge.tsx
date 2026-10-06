import type { CampaignStatus } from "@/lib/api"
import { cn } from "@/lib/utils"

const STYLE: Record<CampaignStatus, string> = {
  draft: "border-dashed border-border text-muted-foreground",
  active: "border-primary/30 bg-primary/10 text-primary",
  paused: "border-ev-issue/40 bg-ev-issue/10 text-foreground",
  ended: "border-border bg-muted text-muted-foreground",
}

const LABEL: Record<CampaignStatus, string> = {
  draft: "Draft",
  active: "Active",
  paused: "Paused",
  ended: "Ended",
}

export function CampaignStatusBadge({
  status,
  className,
}: {
  status: CampaignStatus
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
      {status === "active" && <span className="size-1.5 bg-primary" />}
      {LABEL[status] ?? status}
    </span>
  )
}
