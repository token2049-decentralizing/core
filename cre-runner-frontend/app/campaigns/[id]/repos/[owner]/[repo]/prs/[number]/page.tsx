import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"
import {
  RiExternalLinkLine,
  RiGitCommitLine,
  RiInboxLine,
} from "@remixicon/react"

import {
  attempt,
  getCampaign,
  isExecutionActive,
  listPRExecutions,
  type Campaign,
  type Execution,
} from "@/lib/api"
import {
  formatDateTime,
  formatDuration,
  formatUnits,
  timeAgo,
  TOKEN_DECIMALS,
} from "@/lib/format"
import { ApiErrorState } from "@/components/api-error-state"
import { AutoRefresh } from "@/components/auto-refresh"
import { ExecutionStatusBadge } from "@/components/execution-status-badge"
import { PageBody, PageHeader, Section } from "@/components/page"
import { Pager } from "@/components/pager"
import { Button } from "@/components/ui/button"
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

const PAGE_SIZE = 20

type Params = { id: string; owner: string; repo: string; number: string }

function parseParams({ id, owner, repo, number }: Params) {
  const pr = Number(number)
  return {
    id,
    repo: `${decodeURIComponent(owner)}/${decodeURIComponent(repo)}`,
    pr: Number.isInteger(pr) && pr > 0 ? pr : null,
  }
}

export async function generateMetadata(
  props: PageProps<"/campaigns/[id]/repos/[owner]/[repo]/prs/[number]">
): Promise<Metadata> {
  const { repo, pr } = parseParams(await props.params)
  return { title: `${repo}#${pr ?? "?"} executions` }
}

export default async function PRExecutionsPage(
  props: PageProps<"/campaigns/[id]/repos/[owner]/[repo]/prs/[number]">
) {
  const { id, repo, pr } = parseParams(await props.params)
  if (pr === null) notFound()
  const search = await props.searchParams
  const pageParam = Array.isArray(search.page) ? search.page[0] : search.page
  const page = Math.max(0, Number.parseInt(pageParam ?? "0", 10) || 0)

  const [campaign, executions] = await Promise.all([
    attempt(getCampaign(id)),
    attempt(listPRExecutions(id, repo, pr, { page, page_size: PAGE_SIZE })),
  ])

  const title = `${repo}#${pr}`
  if (!campaign.ok || !executions.ok) {
    const error = !campaign.ok
      ? campaign.error
      : executions.ok
        ? null
        : executions.error
    if (error?.status === 404) notFound()
    return (
      <PageBody>
        <PageHeader title={title} />
        <ApiErrorState
          status={error?.status ?? 500}
          message={error?.message ?? ""}
        />
      </PageBody>
    )
  }

  const rows = executions.data.data
  const active = rows.some(isExecutionActive)

  return (
    <PageBody>
      <AutoRefresh active={active} />
      <PageHeader
        title={title}
        description={
          <span>
            CRE executions for this pull request in{" "}
            <Link
              href={`/campaigns/${campaign.data.id}`}
              className="text-foreground underline-offset-4 hover:underline"
            >
              {campaign.data.name}
            </Link>
            .{active && " Refreshing while executions are in progress."}
          </span>
        }
        actions={
          <>
            <Button
              variant="outline"
              nativeButton={false}
              render={<Link href={`/repos/${repo}?number=${pr}`} />}
            >
              Webhook events
            </Button>
            <Button
              variant="outline"
              nativeButton={false}
              render={
                <a
                  href={`https://github.com/${repo}/pull/${pr}`}
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

      <Summary
        campaign={campaign.data}
        executions={rows}
        total={executions.data.pagination.total}
      />

      <Section title="Executions">
        {rows.length === 0 ? (
          <Empty className="border py-14">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <RiInboxLine />
              </EmptyMedia>
              <EmptyTitle>No executions yet</EmptyTitle>
              <EmptyDescription>
                An execution starts when this pull request is opened, pushed to
                or merged while the campaign is active.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <>
            <ExecutionTable campaign={campaign.data} executions={rows} />
            <Pager pagination={executions.data.pagination} noun="executions" />
          </>
        )}
      </Section>
    </PageBody>
  )
}

function reward(campaign: Campaign, baseUnits: string | null) {
  if (baseUnits === null) return "—"
  const decimals = TOKEN_DECIMALS[campaign.reward_asset]
  return decimals === undefined
    ? baseUnits
    : `${formatUnits(baseUnits, decimals)} ${campaign.reward_asset}`
}

function Summary({
  campaign,
  executions,
  total,
}: {
  campaign: Campaign
  executions: Execution[]
  total: number
}) {
  // Newest first; a settled execution is the payout for this PR.
  const latest = executions[0]
  const settled = executions.find((e) => e.settled)

  return (
    <dl className="grid grid-cols-2 border-y md:grid-cols-4 md:divide-x">
      <Fact label="Latest status">
        {latest ? (
          <ExecutionStatusBadge
            status={latest.status}
            className="h-6 px-2 text-xs"
          />
        ) : (
          "—"
        )}
      </Fact>
      <Fact
        label="Latest score"
        unit={latest?.score != null ? "/ 100" : undefined}
      >
        {latest?.score ?? "—"}
      </Fact>
      <Fact
        label="Settled reward"
        unit={settled ? `by ${settled.id.slice(0, 8)}` : "not settled"}
      >
        {settled ? reward(campaign, settled.reward) : "—"}
      </Fact>
      <Fact label="Executions">{total}</Fact>
    </dl>
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

function ExecutionTable({
  campaign,
  executions,
}: {
  campaign: Campaign
  executions: Execution[]
}) {
  // Rendered per request on the server: running durations count up to now.
  const now = new Date().toISOString()
  return (
    <div className="border">
      <Table className="text-xs">
        <TableHeader>
          <TableRow>
            <TableHead>Execution</TableHead>
            <TableHead>Event</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="text-end">Score</TableHead>
            <TableHead>Reward</TableHead>
            <TableHead>Commit</TableHead>
            <TableHead className="text-end">Duration</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {executions.map((e) => (
            <TableRow key={e.id} className="align-top">
              <TableCell>
                <div className="flex flex-col gap-0.5">
                  <span className="font-medium" title={e.id}>
                    {e.id.slice(0, 8)}
                  </span>
                  <span
                    className="text-muted-foreground"
                    title={formatDateTime(e.created_at)}
                  >
                    {timeAgo(e.created_at)} ·{" "}
                    <Link
                      href={`/deliveries/${e.delivery_id}`}
                      className="underline-offset-4 hover:underline"
                    >
                      webhook
                    </Link>
                  </span>
                </div>
              </TableCell>
              <TableCell>
                {e.event === "merged" ? "Merged" : "Preview"}
              </TableCell>
              <TableCell className="max-w-72 whitespace-normal">
                <div className="flex flex-col items-start gap-1">
                  <ExecutionStatusBadge status={e.status} />
                  {e.error && (
                    <span
                      className={
                        e.status === "failed"
                          ? "break-words text-destructive"
                          : "break-words text-muted-foreground"
                      }
                    >
                      {e.error}
                    </span>
                  )}
                </div>
              </TableCell>
              <TableCell className="text-end tabular-nums">
                {e.score ?? "—"}
              </TableCell>
              <TableCell>
                <div className="flex flex-col gap-0.5">
                  <span className="tabular-nums">
                    {reward(campaign, e.reward)}
                  </span>
                  {e.eligible !== null && (
                    <span className="text-muted-foreground">
                      {e.settled
                        ? "Settled"
                        : e.eligible
                          ? "Eligible"
                          : "Not eligible"}
                    </span>
                  )}
                </div>
              </TableCell>
              <TableCell>
                {e.head_sha ? (
                  <a
                    href={`https://github.com/${e.repository_full_name}/commit/${e.head_sha}`}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1 underline-offset-4 hover:underline"
                    title={
                      e.evaluation_hash
                        ? `evaluation ${e.evaluation_hash}`
                        : undefined
                    }
                  >
                    <RiGitCommitLine className="size-3.5" />
                    {e.head_sha.slice(0, 7)}
                  </a>
                ) : (
                  "—"
                )}
              </TableCell>
              <TableCell className="text-end text-muted-foreground tabular-nums">
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
