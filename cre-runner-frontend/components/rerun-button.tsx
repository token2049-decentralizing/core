"use client"

import * as React from "react"
import { useRouter } from "next/navigation"
import { toast } from "sonner"
import { RiRestartLine } from "@remixicon/react"

import {
  ApiError,
  isExecutionActive,
  rerunExecution,
  type Execution,
} from "@/lib/api"
import { useWalletSession } from "@/components/require-wallet"
import { Button } from "@/components/ui/button"

// Re-runs an execution as a new one, at the PR's current head. Needs a signed-in wallet.
export function RerunButton({
  execution,
  label = "Re-run",
}: {
  execution: Pick<Execution, "id" | "status" | "event" | "settled">
  label?: string
}) {
  const router = useRouter()
  const session = useWalletSession()
  const [pending, setPending] = React.useState(false)

  if (!session.ready || !session.signedIn) return null

  const blocked = isExecutionActive(execution as Execution)
    ? `Execution is still ${execution.status}`
    : execution.event === "merged" && execution.settled
      ? "Already settled: a rerun can't pay again"
      : null

  async function rerun() {
    setPending(true)
    try {
      const { id } = await rerunExecution(execution.id)
      toast.success("Rerun started")
      router.push(`/executions/${id}`)
    } catch (e) {
      toast.error(
        e instanceof ApiError ? e.message : "Couldn't start the rerun."
      )
      setPending(false)
    }
  }

  return (
    <Button
      variant="outline"
      disabled={pending || blocked !== null}
      title={blocked ?? "Evaluate this pull request again at its current head"}
      onClick={rerun}
    >
      <RiRestartLine data-icon="inline-start" />
      {pending ? "Starting…" : label}
    </Button>
  )
}
