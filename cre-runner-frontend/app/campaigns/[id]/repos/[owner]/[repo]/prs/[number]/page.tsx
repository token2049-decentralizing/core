import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"
import { RiExternalLinkLine } from "@remixicon/react"

import {
  attempt,
  getCampaign,
  isExecutionActive,
  listPRExecutions,
  type Campaign,
  type Execution,
} from "@/lib/api"
import { formatReward } from "@/lib/format"
import { ApiErrorState } from "@/components/api-error-state"
import { AutoRefresh } from "@/components/auto-refresh"
import { ExecutionStatusBadge } from "@/components/execution-status-badge"
import { ExecutionTable } from "@/components/execution-table"
import { LivePRStatus } from "@/components/live-pr-status"
import { PageBody, PageHeader, Section } from "@/components/page"
import { Pager } from "@/components/pager"
import { RerunButton } from "@/components/rerun-button"
import { Button } from "@/components/ui/button"

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
  // Rendered per request on the server: running durations count up to now.
  const now = new Date().toISOString()

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
            {rows[0] && (
              <RerunButton execution={rows[0]} label="Re-run latest" />
            )}
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

      <Section title="Live PR status">
        <LivePRStatus
          repo={repo}
          number={pr}
          eligibility={campaign.data.eligibility}
        />
      </Section>

      <Section title="Executions">
        <ExecutionTable
          executions={rows}
          now={now}
          showPR={false}
          showCampaign={false}
          emptyHint="An execution starts when this pull request is opened, pushed to or merged while the campaign is active."
        />
        {rows.length > 0 && (
          <Pager pagination={executions.data.pagination} noun="executions" />
        )}
      </Section>
    </PageBody>
  )
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
        {settled ? formatReward(settled.reward, campaign.reward_asset) : "—"}
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
