import type { Metadata } from "next"
import Link from "next/link"
import { RiAddLine, RiTrophyLine } from "@remixicon/react"

import {
  attempt,
  CAMPAIGN_STATUSES,
  listCampaigns,
  listRepos,
  type CampaignStatus,
} from "@/lib/api"
import { formatAmount, formatDate } from "@/lib/format"
import { ApiErrorState } from "@/components/api-error-state"
import { CampaignFilters } from "@/components/campaign-filters"
import { CampaignStatusBadge } from "@/components/campaign-status-badge"
import { PageBody, PageHeader } from "@/components/page"
import { Pager } from "@/components/pager"
import { Button } from "@/components/ui/button"
import {
  Empty,
  EmptyContent,
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

export const metadata: Metadata = { title: "Campaigns" }

const PAGE_SIZE = 20

function one(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value
}

function schedule(starts: string | null, ends: string | null) {
  if (starts && ends) return `${formatDate(starts)} to ${formatDate(ends)}`
  if (starts) return `From ${formatDate(starts)}`
  if (ends) return `Until ${formatDate(ends)}`
  return "Open-ended"
}

export default async function CampaignsPage(props: PageProps<"/campaigns">) {
  const search = await props.searchParams
  const page = Math.max(0, Number.parseInt(one(search.page) ?? "0", 10) || 0)
  const rawStatus = one(search.status)
  const status = CAMPAIGN_STATUSES.includes(rawStatus as CampaignStatus)
    ? (rawStatus as CampaignStatus)
    : undefined
  const repo = one(search.repo) || undefined

  const [campaigns, repos] = await Promise.all([
    attempt(listCampaigns({ page, page_size: PAGE_SIZE, status, repo })),
    attempt(listRepos()),
  ])

  const isFiltered = Boolean(status || repo)

  return (
    <PageBody>
      <PageHeader
        title="Campaigns"
        description="Each campaign sets a budget and the rules for rewarding merged pull requests in its repositories."
        actions={
          <Button nativeButton={false} render={<Link href="/campaigns/new" />}>
            <RiAddLine data-icon="inline-start" />
            New campaign
          </Button>
        }
      />

      <section className="flex flex-col gap-3">
        <CampaignFilters
          repos={repos.ok ? repos.data.map((r) => r.repository_full_name) : []}
        />

        {!campaigns.ok ? (
          <ApiErrorState
            status={campaigns.error.status}
            message={campaigns.error.message}
          />
        ) : campaigns.data.data.length === 0 ? (
          <Empty className="border py-16">
            <EmptyHeader>
              <EmptyMedia variant="icon">
                <RiTrophyLine />
              </EmptyMedia>
              <EmptyTitle>
                {isFiltered
                  ? "No campaigns match these filters"
                  : "No campaigns yet"}
              </EmptyTitle>
              <EmptyDescription>
                {isFiltered
                  ? "Try another status or repository."
                  : "Create a campaign to start rewarding contributors for merged pull requests."}
              </EmptyDescription>
            </EmptyHeader>
            {!isFiltered && (
              <EmptyContent>
                <Button
                  nativeButton={false}
                  render={<Link href="/campaigns/new" />}
                >
                  <RiAddLine data-icon="inline-start" />
                  New campaign
                </Button>
              </EmptyContent>
            )}
          </Empty>
        ) : (
          <>
            <div className="border">
              <Table>
                <TableHeader>
                  <TableRow className="hover:bg-transparent">
                    <TableHead className="ps-4">Campaign</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead className="text-end">Budget</TableHead>
                    <TableHead className="hidden text-end lg:table-cell">
                      Max per PR
                    </TableHead>
                    <TableHead className="hidden md:table-cell">
                      Repositories
                    </TableHead>
                    <TableHead className="hidden pe-4 xl:table-cell">
                      Schedule
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {campaigns.data.data.map((campaign) => (
                    <TableRow key={campaign.id} className="relative">
                      <TableCell className="max-w-72 ps-4">
                        <Link
                          href={`/campaigns/${campaign.id}`}
                          className="block truncate font-medium outline-none after:absolute after:inset-0 focus-visible:underline"
                        >
                          {campaign.name}
                        </Link>
                        <span className="block truncate text-muted-foreground">
                          {campaign.sponsor ?? "No sponsor listed"}
                        </span>
                      </TableCell>
                      <TableCell>
                        <CampaignStatusBadge status={campaign.status} />
                      </TableCell>
                      <TableCell className="text-end tabular-nums">
                        {formatAmount(campaign.budget, campaign.reward_asset)}
                      </TableCell>
                      <TableCell className="hidden text-end tabular-nums lg:table-cell">
                        {formatAmount(
                          campaign.max_reward_per_pr,
                          campaign.reward_asset
                        )}
                      </TableCell>
                      <TableCell className="hidden max-w-56 md:table-cell">
                        <span
                          className="block truncate"
                          title={campaign.repos.join(", ")}
                        >
                          {campaign.repos.length === 0 ? (
                            <span className="text-muted-foreground">None</span>
                          ) : (
                            <>
                              {campaign.repos[0]}
                              {campaign.repos.length > 1 && (
                                <span className="text-muted-foreground">
                                  {" "}
                                  +{campaign.repos.length - 1}
                                </span>
                              )}
                            </>
                          )}
                        </span>
                      </TableCell>
                      <TableCell className="hidden pe-4 text-muted-foreground xl:table-cell">
                        {schedule(campaign.starts_at, campaign.ends_at)}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            <Pager pagination={campaigns.data.pagination} noun="campaigns" />
          </>
        )}
      </section>
    </PageBody>
  )
}
