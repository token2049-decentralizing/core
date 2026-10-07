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

export const TOKEN_DECIMALS: Record<string, number> = { USDC: 6, SOL: 9 }

// Converts token base units ("445000000") to whole tokens ("445") with string math,
// since rewards can exceed Number's safe integer range.
export function formatUnits(baseUnits: string, decimals: number) {
  if (!/^\d+$/.test(baseUnits)) return baseUnits
  const padded = baseUnits.padStart(decimals + 1, "0")
  const whole = padded.slice(0, padded.length - decimals)
  const frac = padded.slice(padded.length - decimals).replace(/0+$/, "")
  const grouped = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",")
  return frac ? `${grouped}.${frac}` : grouped
}

// Reward in base units as "445 USDC"; unknown assets show raw base units.
export function formatReward(baseUnits: string | null, asset?: string) {
  if (baseUnits === null) return "—"
  const decimals = asset ? TOKEN_DECIMALS[asset] : undefined
  return decimals === undefined
    ? baseUnits
    : `${formatUnits(baseUnits, decimals)} ${asset}`
}

export function formatDuration(fromIso: string, toIso: string) {
  const seconds = Math.max(
    0,
    Math.round((new Date(toIso).getTime() - new Date(fromIso).getTime()) / 1000)
  )
  if (seconds < 60) return `${seconds}s`
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`
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
