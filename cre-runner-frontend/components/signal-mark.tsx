import { cn } from "@/lib/utils"

// Three bars in the event-family colours: the "signal" motif used across the app.
export function SignalMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      aria-hidden="true"
      className={cn("text-foreground", className)}
    >
      <rect width="24" height="24" fill="currentColor" />
      <rect x="5" y="11" width="3" height="8" fill="var(--ev-push)" />
      <rect x="10.5" y="5" width="3" height="14" fill="var(--ev-pr)" />
      <rect x="16" y="8" width="3" height="11" fill="var(--ev-issue)" />
    </svg>
  )
}
