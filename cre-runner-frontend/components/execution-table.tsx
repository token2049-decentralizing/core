import Link from "next/link"
import { RiGitCommitLine, RiInboxLine } from "@remixicon/react"

import type { Execution } from "@/lib/api"
import {
  formatDateTime,
  formatDuration,
  formatReward,
  timeAgo,
} from "@/lib/format"
import { explorerTxUrl } from "@/lib/solana"
import { cn } from "@/lib/utils"
import { ExecutionStatusBadge } from "@/components/execution-status-badge"
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@/components/ui/empty"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"

export function prExecutionsHref(e: Execution) {
  return `/campaigns/${e.campaign_id}/repos/${e.repository_full_name}/prs/${e.pr_number}`
}

// CRE executions, newest first. Each row opens the execution's detail page.
export function ExecutionTable({
  executions,
  now,
  showPR = true,
  showCampaign = true,
  empty = "No executions yet",
  emptyHint = "An execution starts when a pull request is opened, pushed to or merged in a repository of an active campaign.",
}: {
  executions: Execution[]
  now: string // ISO time used for running durations
  showPR?: boolean
  showCampaign?: boolean
  empty?: string
  emptyHint?: string
}) {
  if (executions.length === 0) {
    return (
      <Empty className="border py-14">
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <RiInboxLine />
          </EmptyMedia>
          <EmptyTitle>{empty}</EmptyTitle>
          <EmptyDescription>{emptyHint}</EmptyDescription>
        </EmptyHeader>
      </Empty>
    )
  }

  return (
    <div className="border">
      <Table className="text-xs">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="ps-4">Execution</TableHead>
            {showPR && <TableHead>Pull request</TableHead>}
            {showCampaign && (
              <TableHead className="hidden lg:table-cell">Campaign</TableHead>
            )}
            <TableHead>Status</TableHead>
            <TableHead className="text-end">Score</TableHead>
            <TableHead>Reward</TableHead>
            <TableHead className="hidden md:table-cell">Commit</TableHead>
            <TableHead className="pe-4 text-end">Duration</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {executions.map((e) => (
            <TableRow key={e.id} className="relative align-top">
              <TableCell className="ps-4">
                <div className="flex flex-col gap-0.5">
                  <Link
                    href={`/executions/${e.id}`}
                    className="font-medium outline-none after:absolute after:inset-0 focus-visible:underline"
                    title={e.id}
                  >
                    {e.id.slice(0, 8)}
                  </Link>
                  <span
                    className="text-muted-foreground"
                    title={formatDateTime(e.created_at)}
                  >
                    {e.event === "merged" ? "Merged" : "Preview"} ·{" "}
                    {timeAgo(e.created_at)}
                  </span>
                </div>
              </TableCell>
              {showPR && (
                <TableCell>
                  <div className="flex flex-col gap-0.5">
                    <Link
                      href={prExecutionsHref(e)}
                      className="relative z-10 underline-offset-4 hover:underline"
                    >
                      {e.repository_full_name}#{e.pr_number}
                    </Link>
                    {e.author_login && (
                      <span className="text-muted-foreground">
                        by @{e.author_login}
                      </span>
                    )}
                  </div>
                </TableCell>
              )}
              {showCampaign && (
                <TableCell className="hidden max-w-48 truncate lg:table-cell">
                  {e.campaign ? (
                    <Link
                      href={`/campaigns/${e.campaign_id}`}
                      className="relative z-10 underline-offset-4 hover:underline"
                    >
                      {e.campaign.name}
                    </Link>
                  ) : (
                    <span className="text-muted-foreground">—</span>
                  )}
                </TableCell>
              )}
              <TableCell className="max-w-72 whitespace-normal">
                <div className="flex flex-col items-start gap-1">
                  <ExecutionStatusBadge status={e.status} />
                  {e.error && (
                    <span
                      className={cn(
                        "line-clamp-2 break-words",
                        e.status === "failed"
                          ? "text-destructive"
                          : "text-muted-foreground"
                      )}
                      title={e.error}
                    >
                      {e.error}
                    </span>
                  )}
                </div>
              </TableCell>
              <TableCell className="text-end font-medium tabular-nums">
                {e.score ?? "—"}
              </TableCell>
              <TableCell>
                <div className="flex flex-col gap-0.5">
                  <span className="tabular-nums">
                    {formatReward(e.reward, e.campaign?.reward_asset)}
                  </span>
                  {e.payout_tx ? (
                    <a
                      href={explorerTxUrl(e.payout_tx)}
                      target="_blank"
                      rel="noreferrer"
                      className="relative z-10 text-ev-push underline-offset-4 hover:underline"
                    >
                      Paid on Solana ↗
                    </a>
                  ) : (
                    e.eligible !== null && (
                      <span
                        className={cn(
                          e.settled ? "text-ev-push" : "text-muted-foreground"
                        )}
                      >
                        {e.settled
                          ? "Settled"
                          : e.eligible
                            ? "Eligible"
                            : "Not eligible"}
                      </span>
                    )
                  )}
                </div>
              </TableCell>
              <TableCell className="hidden md:table-cell">
                {e.head_sha ? (
                  <span className="inline-flex items-center gap-1 text-muted-foreground">
                    <RiGitCommitLine className="size-3.5" />
                    {e.head_sha.slice(0, 7)}
                  </span>
                ) : (
                  "—"
                )}
              </TableCell>
              <TableCell className="pe-4 text-end text-muted-foreground tabular-nums">
                {e.started_at
                  ? formatDuration(e.started_at, e.finished_at ?? now)
                  : "—"}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  )
}
