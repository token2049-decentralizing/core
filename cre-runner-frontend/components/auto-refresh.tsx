"use client"

import * as React from "react"
import { useRouter } from "next/navigation"

// Re-renders the server page every few seconds while `active` is true,
// e.g. until queued and running executions finish.
export function AutoRefresh({
  active,
  intervalMs = 3000,
}: {
  active: boolean
  intervalMs?: number
}) {
  const router = useRouter()

  React.useEffect(() => {
    if (!active) return
    const timer = setInterval(() => router.refresh(), intervalMs)
    return () => clearInterval(timer)
  }, [active, intervalMs, router])

  return null
}
