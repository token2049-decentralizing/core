import type { RepoEvent } from "@/lib/api"
import { eventColor, eventTone, formatDateTime, TONE_VAR } from "@/lib/format"
import { cn } from "@/lib/utils"

const TONE_LABEL: Record<keyof typeof TONE_VAR, string> = {
  pr: "Pull requests",
  push: "Pushes and branches",
  comment: "Comments",
  issue: "Issues",
  ci: "Checks and workflows",
  other: "Other",
}

function describe(event: RepoEvent) {
  const what = event.action ? `${event.event} ${event.action}` : event.event
  const who = event.sender ? ` by ${event.sender.login}` : ""
  return `${what}${who}, ${formatDateTime(event.received_at)}`
}

// One bar per received webhook, oldest on the left, coloured by event family.
export function SignalStrip({
  events,
  size = "lg",
  legend = false,
  className,
}: {
  events: RepoEvent[]
  size?: "sm" | "lg"
  legend?: boolean
  className?: string
}) {
  const ordered = [...events].reverse()
  const counts = new Map<keyof typeof TONE_VAR, number>()
  for (const event of events) {
    const tone = eventTone(event.event)
    counts.set(tone, (counts.get(tone) ?? 0) + 1)
  }

  return (
    <div className={cn("flex flex-col gap-2", className)}>
      <div
        role="img"
        aria-label={`Last ${events.length} events`}
        className={cn(
          "flex items-end",
          size === "lg" ? "h-16 gap-[3px]" : "h-5 gap-px"
        )}
      >
        {ordered.map((event, i) => (
          <span
            key={event.id}
            data-signal-bar
            title={describe(event)}
            className={cn(
              "h-full min-w-px flex-1 origin-bottom",
              size === "lg" ? "max-w-2.5" : "max-w-1"
            )}
            style={{
              background: eventColor(event.event),
              animation:
                size === "lg"
                  ? `signal-in 420ms cubic-bezier(0.2, 0.7, 0.2, 1) ${i * 12}ms both`
                  : undefined,
            }}
          />
        ))}
        {size === "lg" && ordered.length === 0 && (
          <span className="h-full flex-1 border border-dashed" />
        )}
      </div>
      {legend && counts.size > 0 && (
        <ul className="flex flex-wrap gap-x-4 gap-y-1 text-[11px] text-muted-foreground">
          {[...counts.entries()]
            .sort((a, b) => b[1] - a[1])
            .map(([tone, count]) => (
              <li key={tone} className="flex items-center gap-1.5">
                <span
                  className="size-2"
                  style={{ background: TONE_VAR[tone] }}
                />
                {TONE_LABEL[tone]}
                <span className="text-foreground tabular-nums">{count}</span>
              </li>
            ))}
        </ul>
      )}
    </div>
  )
}
