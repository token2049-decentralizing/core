import Link from "next/link"
import { RiAddLine, RiInboxLine } from "@remixicon/react"

import {
  attempt,
  listCampaigns,
  listRepoEvents,
  listRepos,
  type RepoEvent,
} from "@/lib/api"
import { formatAmount, formatCount, formatDate, timeAgo } from "@/lib/format"
import { ApiErrorState } from "@/components/api-error-state"
import { CampaignStatusBadge } from "@/components/campaign-status-badge"
import { PageBody, PageHeader, Section } from "@/components/page"
import { SignalStrip } from "@/components/signal-strip"
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

const STRIP_REPOS = 8
const STRIP_EVENTS = 48

export default async function OverviewPage() {
  const [repos, recent, active] = await Promise.all([
    attempt(listRepos()),
    attempt(listCampaigns({ page_size: 5 })),
    attempt(listCampaigns({ status: "active", page_size: 1 })),
  ])

  if (!repos.ok) {
    return (
      <PageBody>
        <PageHeader title="Overview" />
        <ApiErrorState
          status={repos.error.status}
          message={repos.error.message}
        />
      </PageBody>
    )
  }

  const strips = new Map<string, RepoEvent[]>()
  await Promise.all(
    repos.data.slice(0, STRIP_REPOS).map(async (repo) => {
      const res = await attempt(
        listRepoEvents(repo.repository_full_name, { page_size: STRIP_EVENTS })
      )
      if (res.ok) strips.set(repo.repository_full_name, res.data.data)
    })
  )

  const totalEvents = repos.data.reduce((sum, r) => sum + r.event_count, 0)
  const activeCount = active.ok ? active.data.pagination.total : null

  return (
    <PageBody>
      <PageHeader
        title="Overview"
        description={
          repos.data.length === 0 ? (
            "No webhooks yet. Install the GitHub App on a repository to start receiving events."
          ) : (
            <>
              {formatCount(totalEvents)} events from {repos.data.length}{" "}
              {repos.data.length === 1 ? "repository" : "repositories"}
              {activeCount !== null && (
                <>
                  , {activeCount} active{" "}
                  {activeCount === 1 ? "campaign" : "campaigns"}
                </>
              )}
              . Bot activity is filtered out.
            </>
          )
        }
        actions={
          <Button nativeButton={false} render={<Link href="/campaigns/new" />}>
            <RiAddLine data-icon="inline-start" />
            New campaign
          </Button>
        }
      />

      <Section title="Repositories">
        {repos.data.length === 0 ? (
          <Empty className="border py-14">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <RiInboxLine />
              </EmptyMedia>
              <EmptyTitle>Waiting for the first webhook</EmptyTitle>
              <EmptyDescription>
                Repositories show up here as soon as cre-runner receives an
                event from a person (not a bot).
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="border">
            <Table>
              <TableHeader>
                <TableRow className="hover:bg-transparent">
                  <TableHead className="ps-4">Repository</TableHead>
                  <TableHead className="hidden w-[40%] md:table-cell">
                    Recent activity
                  </TableHead>
                  <TableHead className="text-end">Events</TableHead>
                  <TableHead className="pe-4 text-end">Last event</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {repos.data.map((repo) => {
                  const strip = strips.get(repo.repository_full_name)
                  return (
                    <TableRow
                      key={repo.repository_full_name}
                      className="relative"
                    >
                      <TableCell className="ps-4 font-medium">
                        <Link
                          href={`/repos/${repo.repository_full_name}`}
                          className="outline-none after:absolute after:inset-0 focus-visible:underline"
                        >
                          {repo.repository_full_name}
                        </Link>
                      </TableCell>
                      <TableCell className="hidden md:table-cell">
                        {strip ? (
                          <SignalStrip events={strip} size="sm" />
                        ) : (
                          <span className="text-muted-foreground">–</span>
                        )}
                      </TableCell>
                      <TableCell className="text-end tabular-nums">
                        {formatCount(repo.event_count)}
                      </TableCell>
                      <TableCell
                        className="pe-4 text-end text-muted-foreground"
                        title={repo.last_event_at}
                      >
                        {timeAgo(repo.last_event_at)}
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
      </Section>

      <Section
        title="Latest campaigns"
        actions={
          recent.ok &&
          recent.data.pagination.total > recent.data.data.length && (
            <Link
              href="/campaigns"
              className="text-xs text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
            >
              See all {recent.data.pagination.total}
            </Link>
          )
        }
      >
        {!recent.ok ? (
          <ApiErrorState
            status={recent.error.status}
            message={recent.error.message}
          />
        ) : recent.data.data.length === 0 ? (
          <div className="flex flex-col items-start gap-3 border border-dashed p-6 text-xs text-muted-foreground">
            <p>
              No campaigns yet. A campaign sets a budget and rules for rewarding
              merged pull requests.
            </p>
            <Button
              variant="outline"
              nativeButton={false}
              render={<Link href="/campaigns/new" />}
            >
              Create a campaign
            </Button>
          </div>
        ) : (
          <ul className="divide-y border">
            {recent.data.data.map((campaign) => (
              <li key={campaign.id} className="relative">
                <div className="flex flex-col gap-2 px-4 py-3 hover:bg-muted/50 sm:flex-row sm:items-center sm:gap-6">
                  <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                    <Link
                      href={`/campaigns/${campaign.id}`}
                      className="truncate text-sm font-medium outline-none after:absolute after:inset-0 focus-visible:underline"
                    >
                      {campaign.name}
                    </Link>
                    <span className="truncate text-xs text-muted-foreground">
                      {campaign.sponsor ? `${campaign.sponsor}, ` : ""}
                      {campaign.repos.length}{" "}
                      {campaign.repos.length === 1
                        ? "repository"
                        : "repositories"}
                    </span>
                  </div>
                  <div className="flex items-center gap-4 text-xs">
                    <span className="tabular-nums">
                      {formatAmount(campaign.budget, campaign.reward_asset)}
                    </span>
                    <span className="hidden w-24 text-muted-foreground lg:inline">
                      {campaign.ends_at
                        ? `Ends ${formatDate(campaign.ends_at)}`
                        : "No end date"}
                    </span>
                    <CampaignStatusBadge status={campaign.status} />
                  </div>
                </div>
              </li>
            ))}
          </ul>
        )}
      </Section>
    </PageBody>
  )
}
