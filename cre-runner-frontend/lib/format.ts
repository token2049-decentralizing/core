const relative = new Intl.RelativeTimeFormat("en", { numeric: "auto" })

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
]

export function timeAgo(iso: string, now = Date.now()) {
  const seconds = (new Date(iso).getTime() - now) / 1000
  for (const [unit, size] of UNITS) {
    if (Math.abs(seconds) >= size) {
      return relative.format(Math.round(seconds / size), unit)
    }
  }
  return "just now"
}

const dateTime = new Intl.DateTimeFormat("en", {
  year: "numeric",
  month: "short",
  day: "numeric",
  hour: "2-digit",
  minute: "2-digit",
  hour12: false,
})

const dateOnly = new Intl.DateTimeFormat("en", {
  year: "numeric",
  month: "short",
  day: "numeric",
})

export function formatDateTime(iso: string) {
  return dateTime.format(new Date(iso))
}

export function formatDate(iso: string) {
  return dateOnly.format(new Date(iso))
}

export function formatAmount(value: number, asset?: string) {
  const amount = new Intl.NumberFormat("en", {
    maximumFractionDigits: 6,
  }).format(value)
  return asset ? `${amount} ${asset}` : amount
}

export function formatCount(value: number) {
  return new Intl.NumberFormat("en").format(value)
}

export function humanize(key: string) {
  const text = key.replace(/[_-]+/g, " ").trim()
  return text.charAt(0).toUpperCase() + text.slice(1)
}

// Each GitHub event family gets one hue; see --ev-* in globals.css.
export function eventTone(event: string) {
  if (event.startsWith("pull_request")) return "pr"
  if (event === "push" || event === "create" || event === "delete")
    return "push"
  if (event.includes("comment")) return "comment"
  if (event.startsWith("issue")) return "issue"
  if (
    event.startsWith("check") ||
    event.startsWith("workflow") ||
    event === "status"
  )
    return "ci"
  return "other"
}

export const TONE_VAR: Record<ReturnType<typeof eventTone>, string> = {
  pr: "var(--ev-pr)",
  push: "var(--ev-push)",
  comment: "var(--ev-comment)",
  issue: "var(--ev-issue)",
  ci: "var(--ev-ci)",
  other: "var(--ev-other)",
}

export function eventColor(event: string) {
  return TONE_VAR[eventTone(event)]
}

export function shortRef(ref: string) {
  return ref.replace(/^refs\/(heads|tags)\//, "")
}
