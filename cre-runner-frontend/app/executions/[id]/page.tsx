import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"
import { RiExternalLinkLine, RiGitCommitLine } from "@remixicon/react"

import { attempt, getExecution, isExecutionActive } from "@/lib/api"
import {
  formatDateTime,
  formatDuration,
  formatReward,
  timeAgo,
} from "@/lib/format"
import { cn } from "@/lib/utils"
import { ApiErrorState } from "@/components/api-error-state"
import { AutoRefresh } from "@/components/auto-refresh"
import { ExecutionStatusBadge } from "@/components/execution-status-badge"
import { prExecutionsHref } from "@/components/execution-table"
import { PageBody, PageHeader, Section } from "@/components/page"
import { Button } from "@/components/ui/button"

export async function generateMetadata(
  props: PageProps<"/executions/[id]">
): Promise<Metadata> {
  const { id } = await props.params
  return { title: `Execution ${id.slice(0, 8)}` }
}

export default async function ExecutionPage(
  props: PageProps<"/executions/[id]">
) {
  const { id } = await props.params
  const result = await attempt(getExecution(id))

  if (!result.ok) {
    if (result.error.status === 404) notFound()
    return (
      <PageBody>
        <PageHeader title="Execution" />
        <ApiErrorState
          status={result.error.status}
          message={result.error.message}
        />
      </PageBody>
    )
  }

  const e = result.data
  const active = isExecutionActive(e)
  const asset = e.campaign?.reward_asset
  // Rendered per request on the server: a running execution's duration counts up to now.
  const now = new Date().toISOString()

  return (
    <PageBody>
      <AutoRefresh active={active} />
      <PageHeader
        title={
          <span className="flex flex-wrap items-center gap-3">
            Execution {e.id.slice(0, 8)}
            <ExecutionStatusBadge
              status={e.status}
              className="h-6 px-2 text-xs"
            />
          </span>
        }
        description={
          <span>
            {e.event === "merged" ? "Settlement run" : "Preview run"} for{" "}
            <Link
              href={prExecutionsHref(e)}
              className="text-foreground underline-offset-4 hover:underline"
            >
              {e.repository_full_name}#{e.pr_number}
            </Link>
            {e.campaign && (
              <>
                {" "}
                in{" "}
                <Link
                  href={`/campaigns/${e.campaign_id}`}
                  className="text-foreground underline-offset-4 hover:underline"
                >
                  {e.campaign.name}
                </Link>
              </>
            )}
            , started {timeAgo(e.created_at)}.
            {active && " Refreshing until it finishes."}
          </span>
        }
        actions={
          <>
            <Button
              variant="outline"
              nativeButton={false}
              render={<Link href={prExecutionsHref(e)} />}
            >
              PR history
            </Button>
            <Button
              variant="outline"
              nativeButton={false}
              render={
                <a
                  href={`https://github.com/${e.repository_full_name}/pull/${e.pr_number}`}
                  target="_blank"
                  rel="noreferrer"
                />
              }
            >
              <RiExternalLinkLine data-icon="inline-start" />
              GitHub
            </Button>
          </>
        }
      />

      {e.error && (
        <div
          className={cn(
            "border p-3 text-xs leading-relaxed break-words",
            e.status === "failed"
              ? "border-destructive/30 bg-destructive/5 text-destructive"
              : "bg-muted/50 text-muted-foreground"
          )}
        >
          <p className="mb-1 font-medium">
            {e.status === "failed" ? "Why it failed" : "Note"}
          </p>
          {e.error}
        </div>
      )}

      <dl className="grid grid-cols-2 border-y md:grid-cols-4 md:divide-x">
        <Fact label="Score" unit={e.score !== null ? "/ 100" : undefined}>
          {e.score ?? "—"}
        </Fact>
        <Fact
          label="Eligibility"
          unit={e.event === "opened" ? "previews never pay" : undefined}
        >
          {e.eligible === null ? "—" : e.eligible ? "Eligible" : "Not eligible"}
        </Fact>
        <Fact
          label="Reward"
          unit={
            e.settled
              ? "settled"
              : e.reward !== null
                ? "not settled"
                : undefined
          }
        >
          {formatReward(e.reward, asset)}
        </Fact>
        <Fact label="Duration">
          {e.started_at
            ? formatDuration(e.started_at, e.finished_at ?? now)
            : "—"}
        </Fact>
      </dl>

      <div className="grid gap-10 lg:grid-cols-[1fr_20rem]">
        <div className="flex min-w-0 flex-col gap-10">
          <Section title="Timeline">
            <ol className="flex flex-col gap-3 border-s ps-4 text-xs">
              <Step label="Queued" at={e.created_at} done />
              <Step
                label="Running"
                at={e.started_at}
                done={e.started_at !== null}
              />
              <Step
                label={
                  e.status === "completed"
                    ? "Completed"
                    : e.status === "failed"
                      ? "Failed"
                      : e.status === "skipped"
                        ? "Skipped"
                        : "Finished"
                }
                at={e.finished_at}
                done={e.finished_at !== null}
                tone={e.status === "failed" ? "bad" : undefined}
              />
            </ol>
          </Section>
          <Section title="Workflow request">
            <pre className="overflow-x-auto border bg-muted/40 p-3 text-[11px] leading-relaxed">
              {JSON.stringify(e.request, null, 2)}
            </pre>
          </Section>
        </div>

        <aside>
          <dl className="grid grid-cols-[7rem_1fr] gap-x-3 gap-y-2 text-xs">
            <dt className="text-muted-foreground">Execution ID</dt>
            <dd className="break-all">{e.id}</dd>
            <dt className="text-muted-foreground">Event</dt>
            <dd>
              {e.event === "merged"
                ? "Merged (settlement)"
                : "Opened / pushed (preview)"}
            </dd>
            <dt className="text-muted-foreground">Commit</dt>
            <dd>
              {e.head_sha ? (
                <a
                  href={`https://github.com/${e.repository_full_name}/commit/${e.head_sha}`}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 underline-offset-4 hover:underline"
                >
                  <RiGitCommitLine className="size-3.5" />
                  {e.head_sha.slice(0, 7)}
                </a>
              ) : (
                "—"
              )}
            </dd>
            <dt className="text-muted-foreground">Webhook</dt>
            <dd>
              <Link
                href={`/deliveries/${e.delivery_id}`}
                className="break-all underline-offset-4 hover:underline"
              >
                {e.delivery_id}
              </Link>
            </dd>
            <dt className="text-muted-foreground">Evaluation hash</dt>
            <dd className="break-all">{e.evaluation_hash ?? "—"}</dd>
            <dt className="text-muted-foreground">Policy hash</dt>
            <dd className="break-all">{e.policy_hash ?? "—"}</dd>
            <dt className="text-muted-foreground">Runner</dt>
            <dd className="break-all">{e.runner_instance ?? "—"}</dd>
          </dl>
        </aside>
      </div>
    </PageBody>
  )
}

function Fact({
  label,
  unit,
  children,
}: {
  label: string
  unit?: string
  children: React.ReactNode
}) {
  return (
    <div className="flex flex-col gap-1 py-4 pe-4 md:ps-5 md:first:ps-0">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="flex flex-wrap items-baseline gap-x-1.5">
        <span className="font-heading text-xl font-bold tabular-nums">
          {children}
        </span>
        {unit && (
          <span className="text-[11px] text-muted-foreground">{unit}</span>
        )}
      </dd>
    </div>
  )
}

function Step({
  label,
  at,
  done,
  tone,
}: {
  label: string
  at: string | null
  done: boolean
  tone?: "bad"
}) {
  return (
    <li className="relative">
      <span
        className={cn(
          "absolute -start-[1.3rem] top-1 size-2 border",
          done
            ? tone === "bad"
              ? "border-destructive bg-destructive"
              : "border-primary bg-primary"
            : "border-border bg-background"
        )}
      />
      <span className={cn("font-medium", !done && "text-muted-foreground")}>
        {label}
      </span>
      <span className="ms-2 text-muted-foreground">
        {at ? formatDateTime(at) : "pending"}
      </span>
    </li>
  )
}
