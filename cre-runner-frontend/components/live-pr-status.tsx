"use client"

import * as React from "react"
import {
  RiCheckLine,
  RiCloseLine,
  RiGitCommitLine,
  RiLoopLeftLine,
  RiQuestionLine,
} from "@remixicon/react"

import { ApiError, getPRStatus, type PRStatus } from "@/lib/api"
import { timeAgo } from "@/lib/format"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"

type State =
  | { status: "loading" }
  | { status: "ready"; pr: PRStatus }
  | { status: "error"; message: string }

// Live pull request state from GitHub, re-read on every visit (and on Refresh), judged the
// way the CRE workflow judges it, next to the campaign's eligibility gates.
export function LivePRStatus({
  repo,
  number,
  eligibility,
}: {
  repo: string
  number: number
  eligibility?: Record<string, unknown>
}) {
  const [state, setState] = React.useState<State>({ status: "loading" })
  const [reload, setReload] = React.useState(0)

  React.useEffect(() => {
    let cancelled = false
    getPRStatus(repo, number)
      .then((pr) => !cancelled && setState({ status: "ready", pr }))
      .catch((e: unknown) => {
        if (!cancelled)
          setState({
            status: "error",
            message:
              e instanceof ApiError || e instanceof Error
                ? e.message
                : "Couldn't read the pull request.",
          })
      })
    return () => {
      cancelled = true
    }
  }, [repo, number, reload])

  const refresh = (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => {
        setState({ status: "loading" })
        setReload((n) => n + 1)
      }}
    >
      <RiLoopLeftLine data-icon="inline-start" />
      Refresh
    </Button>
  )

  if (state.status === "loading") return <Skeleton className="h-40 w-full" />
  if (state.status === "error") {
    return (
      <div className="flex items-center justify-between gap-3 border border-destructive/30 bg-destructive/5 p-3 text-xs text-destructive">
        <span className="break-words">{state.message}</span>
        {refresh}
      </div>
    )
  }

  const { pr } = state
  const required = (gate: string) => eligibility?.[gate] === true
  const counted = pr.linked_issues
  const countedNumbers = new Set(counted.map((i) => i.number))
  const githubOnly = (pr.github_linked_issues ?? []).filter(
    (n) => !countedNumbers.has(n)
  )
  const prState = pr.merged
    ? "Merged"
    : pr.draft
      ? "Draft"
      : pr.state === "open"
        ? "Open"
        : "Closed"

  return (
    <div className="flex flex-col gap-3 border p-3 text-xs">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <span
            className={cn(
              "border px-1.5 py-0.5 text-[11px] font-medium",
              pr.merged
                ? "border-ev-pr/40 bg-ev-pr/10"
                : pr.state === "open"
                  ? "border-ev-push/40 bg-ev-push/10"
                  : "bg-muted text-muted-foreground"
            )}
          >
            {prState}
          </span>
          <a
            href={pr.html_url}
            target="_blank"
            rel="noreferrer"
            className="truncate font-medium underline-offset-4 hover:underline"
          >
            {pr.title}
          </a>
          <span className="text-muted-foreground">by @{pr.author_login}</span>
        </div>
        <div className="flex items-center gap-2 text-muted-foreground">
          <span className="inline-flex items-center gap-1">
            <RiGitCommitLine className="size-3.5" />
            {pr.head_sha.slice(0, 7)}
          </span>
          <span title={pr.fetched_at}>checked {timeAgo(pr.fetched_at)}</span>
          {refresh}
        </div>
      </div>

      <ul className="divide-y border-t">
        <Row
          label="Merged"
          ok={pr.merged}
          required={required("merged")}
          detail={
            pr.merged
              ? pr.merged_at
                ? `merged ${timeAgo(pr.merged_at)}`
                : "merged"
              : "not merged yet; only merged PRs are paid"
          }
        />
        <Row
          label="CI"
          ok={
            pr.ci_status === "PASS"
              ? true
              : pr.ci_status === "FAIL"
                ? false
                : null
          }
          required={required("ci_passed")}
          detail={
            pr.checks.length === 0
              ? "no check runs on the head commit"
              : `${pr.checks.filter((c) => c.status === "completed").length}/${pr.checks.length} checks completed` +
                (pr.ci_status === "FAIL"
                  ? `, failing: ${failing(pr)}`
                  : pr.ci_status === "UNKNOWN"
                    ? ", still running"
                    : "")
          }
        />
        <Row
          label="Linked issue"
          ok={counted.length > 0}
          required={required("linked_issue")}
          detail={
            <div className="flex flex-col gap-1">
              {counted.length === 0 ? (
                <span>
                  none counted. Add{" "}
                  <code className="bg-muted px-1">Fixes #123</code> (or closes /
                  resolves) to the PR description, then re-run.
                </span>
              ) : (
                <ul className="flex flex-col gap-0.5">
                  {counted.map((i) => (
                    <li key={i.number}>
                      <a
                        href={i.url}
                        target="_blank"
                        rel="noreferrer"
                        className="underline-offset-4 hover:underline"
                      >
                        #{i.number}
                      </a>{" "}
                      {i.title === null ? (
                        <span className="text-ev-issue">
                          not found or not readable (still counted)
                        </span>
                      ) : (
                        <span className="text-muted-foreground">
                          {i.title} ·{" "}
                          {i.is_pull_request
                            ? "a pull request, not an issue (still counted)"
                            : i.state}
                        </span>
                      )}
                    </li>
                  ))}
                </ul>
              )}
              {githubOnly.length > 0 && (
                <span className="text-ev-issue">
                  GitHub also links {githubOnly.map((n) => `#${n}`).join(", ")}{" "}
                  (e.g. from the Development sidebar), but CRE only counts
                  closing keywords in the PR description.
                </span>
              )}
            </div>
          }
        />
        <Row
          label="Approvals"
          ok={pr.approvals > 0}
          detail={
            pr.approvals > 0
              ? `${pr.approvals} approving reviewer(s)`
              : "no approving review (scores lower, not a gate)"
          }
        />
      </ul>
      <p className="text-[11px] text-muted-foreground">
        What the next evaluation will see. Past executions keep the state they
        ran with; re-run to evaluate this one.
      </p>
    </div>
  )
}

function failing(pr: PRStatus) {
  const names = pr.checks
    .filter(
      (c) =>
        c.status === "completed" &&
        !["success", "neutral", "skipped"].includes(c.conclusion ?? "")
    )
    .map((c) => c.name)
  return names.slice(0, 3).join(", ") + (names.length > 3 ? "…" : "")
}

function Row({
  label,
  ok,
  required,
  detail,
}: {
  label: string
  ok: boolean | null // null: unknown (e.g. CI still running)
  required?: boolean
  detail: React.ReactNode
}) {
  const Icon = ok === null ? RiQuestionLine : ok ? RiCheckLine : RiCloseLine
  return (
    <li className="grid grid-cols-[1.25rem_7rem_1fr] items-start gap-2 py-2">
      <Icon
        className={cn(
          "size-4",
          ok === null
            ? "text-muted-foreground"
            : ok
              ? "text-ev-push"
              : required
                ? "text-destructive"
                : "text-muted-foreground"
        )}
      />
      <span className="font-medium">
        {label}
        {required && (
          <span
            className={cn(
              "ms-1 text-[10px] font-normal",
              ok ? "text-muted-foreground" : "text-destructive"
            )}
          >
            required
          </span>
        )}
      </span>
      <div className="min-w-0 break-words text-muted-foreground">{detail}</div>
    </li>
  )
}
