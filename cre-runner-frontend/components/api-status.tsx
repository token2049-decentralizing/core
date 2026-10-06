"use client"

import * as React from "react"

import { API_BASE_URL } from "@/lib/api"
import { cn } from "@/lib/utils"

type Status = "checking" | "online" | "offline"

const LABEL: Record<Status, string> = {
  checking: "Checking API",
  online: "API connected",
  offline: "API unreachable",
}

export function ApiStatus() {
  const [status, setStatus] = React.useState<Status>("checking")

  React.useEffect(() => {
    let cancelled = false

    async function check() {
      try {
        const res = await fetch(`${API_BASE_URL}/api/repos`, {
          cache: "no-store",
        })
        if (!cancelled) setStatus(res.ok ? "online" : "offline")
      } catch {
        if (!cancelled) setStatus("offline")
      }
    }

    check()
    const timer = window.setInterval(check, 30_000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [])

  const host = API_BASE_URL.replace(/^https?:\/\//, "")

  return (
    <div
      className="flex items-center gap-2 px-2 py-1.5 text-[11px] group-data-[collapsible=icon]:justify-center group-data-[collapsible=icon]:px-0"
      title={`${LABEL[status]}: ${API_BASE_URL}`}
    >
      <span
        className={cn(
          "size-2 shrink-0",
          status === "online" && "bg-ev-push",
          status === "offline" && "bg-destructive",
          status === "checking" && "animate-pulse bg-muted-foreground"
        )}
      />
      <span className="flex min-w-0 flex-col group-data-[collapsible=icon]:hidden">
        <span>{LABEL[status]}</span>
        <span className="truncate text-muted-foreground">{host}</span>
      </span>
    </div>
  )
}
