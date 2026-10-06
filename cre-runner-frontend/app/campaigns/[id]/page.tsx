import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"
import { RiCheckLine, RiCloseLine } from "@remixicon/react"

import { attempt, getCampaign, type Campaign } from "@/lib/api"
import {
  formatAmount,
  formatDate,
  formatDateTime,
  humanize,
} from "@/lib/format"
import { ApiErrorState } from "@/components/api-error-state"
import { CampaignStatusBadge } from "@/components/campaign-status-badge"
import { PageBody, PageHeader, Section } from "@/components/page"

export async function generateMetadata(
  props: PageProps<"/campaigns/[id]">
): Promise<Metadata> {
  const { id } = await props.params
  const result = await attempt(getCampaign(id))
  return { title: result.ok ? result.data.name : "Campaign" }
}

export default async function CampaignPage(
  props: PageProps<"/campaigns/[id]">
) {
  const { id } = await props.params
  const result = await attempt(getCampaign(id))

  if (!result.ok) {
    if (result.error.status === 404) notFound()
    return (
      <PageBody>
        <PageHeader title="Campaign" />
        <ApiErrorState
          status={result.error.status}
          message={result.error.message}
        />
      </PageBody>
    )
  }

  const campaign = result.data

  return (
    <PageBody>
      <PageHeader
        title={campaign.name}
        description={
          <div className="flex flex-col gap-1">
            {campaign.sponsor && <span>Sponsored by {campaign.sponsor}</span>}
            {campaign.description && (
              <p className="text-foreground">{campaign.description}</p>
            )}
          </div>
        }
        actions={
          <CampaignStatusBadge
            status={campaign.status}
            className="h-6 px-2 text-xs"
          />
        }
      />

      <dl className="grid grid-cols-2 border-y md:grid-cols-4 md:divide-x">
        <Fact
          label="Budget"
          value={formatAmount(campaign.budget)}
          unit={campaign.reward_asset}
        />
        <Fact
          label="Max reward per PR"
          value={formatAmount(campaign.max_reward_per_pr)}
          unit={campaign.reward_asset}
        />
        <Fact
          label="Minimum score"
          value={
            campaign.min_score !== null ? String(campaign.min_score) : "None"
          }
          unit={campaign.min_score !== null ? "/ 100" : undefined}
        />
        <Fact
          label="PRs at max reward"
          value={String(
            Math.floor(campaign.budget / campaign.max_reward_per_pr)
          )}
          unit="before the budget runs out"
        />
      </dl>

      <div className="grid gap-10 lg:grid-cols-[1fr_20rem]">
        <div className="flex min-w-0 flex-col gap-10">
          <Section title="Schedule">
            <Schedule campaign={campaign} />
          </Section>
          <Section title="Scoring weights">
            <Scoring scoring={campaign.scoring} />
          </Section>
          <Section title="Eligibility">
            <Eligibility eligibility={campaign.eligibility} />
          </Section>
        </div>

        <aside className="flex flex-col gap-10">
          <Section title={`Repositories (${campaign.repos.length})`}>
            {campaign.repos.length === 0 ? (
              <p className="text-xs text-muted-foreground">
                No repositories attached yet.
              </p>
            ) : (
              <ul className="divide-y border text-xs">
                {campaign.repos.map((repo) => (
                  <li key={repo}>
                    <Link
                      href={`/repos/${repo}`}
                      className="block truncate px-3 py-2 hover:bg-muted/50"
                    >
                      {repo}
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </Section>
          <Section title="Treasury">
            {campaign.treasury_address ? (
              <p className="text-xs break-all">{campaign.treasury_address}</p>
            ) : (
              <p className="text-xs leading-relaxed text-muted-foreground">
                Not funded yet. The Solana treasury address appears here once
                funds are deposited.
              </p>
            )}
          </Section>
          <dl className="grid grid-cols-[6rem_1fr] gap-x-3 gap-y-1.5 text-xs">
            <dt className="text-muted-foreground">Created</dt>
            <dd>{formatDateTime(campaign.created_at)}</dd>
            <dt className="text-muted-foreground">Campaign ID</dt>
            <dd className="break-all">{campaign.id}</dd>
          </dl>
        </aside>
      </div>
    </PageBody>
  )
}

function Fact({
  label,
  value,
  unit,
}: {
  label: string
  value: string
  unit?: string
}) {
  return (
    <div className="flex flex-col gap-1 py-4 pe-4 md:ps-5 md:first:ps-0">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="flex flex-wrap items-baseline gap-x-1.5">
        <span className="font-heading text-xl font-bold tabular-nums">
          {value}
        </span>
        {unit && (
          <span className="text-[11px] text-muted-foreground">{unit}</span>
        )}
      </dd>
    </div>
  )
}

function Schedule({ campaign }: { campaign: Campaign }) {
  const { starts_at, ends_at } = campaign
  if (!starts_at && !ends_at) {
    return (
      <p className="text-xs text-muted-foreground">
        No start or end date. The campaign runs until it is ended manually.
      </p>
    )
  }

  // Rendered per request on the server, so reading the clock here is intended.
  // eslint-disable-next-line react-hooks/purity
  const now = Date.now()
  const start = starts_at ? new Date(starts_at).getTime() : null
  const end = ends_at ? new Date(ends_at).getTime() : null
  const progress =
    start !== null && end !== null
      ? Math.min(1, Math.max(0, (now - start) / (end - start)))
      : null

  let note: string
  if (start !== null && now < start) {
    note = `Starts in ${Math.ceil((start - now) / 86_400_000)} days`
  } else if (end !== null && now > end) {
    note = "Finished"
  } else if (end !== null) {
    note = `${Math.ceil((end - now) / 86_400_000)} days left`
  } else {
    note = "Running, no end date"
  }

  return (
    <div className="flex flex-col gap-2 text-xs">
      <div className="flex justify-between gap-4">
        <span>{starts_at ? formatDate(starts_at) : "Any time"}</span>
        <span className="text-muted-foreground">{note}</span>
        <span>{ends_at ? formatDate(ends_at) : "No end"}</span>
      </div>
      <div className="h-1.5 bg-muted">
        {progress !== null && (
          <div
            className="h-full bg-primary"
            style={{ width: `${progress * 100}%` }}
          />
        )}
      </div>
    </div>
  )
}

function Scoring({ scoring }: { scoring: Record<string, unknown> }) {
  const entries = Object.entries(scoring)
  if (entries.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">No scoring weights set.</p>
    )
  }

  const numeric = entries.filter(
    (e): e is [string, number] => typeof e[1] === "number"
  )
  const total = numeric.reduce((sum, [, v]) => sum + v, 0)
  const max = Math.max(1, ...numeric.map(([, v]) => v))

  return (
    <div className="flex flex-col gap-3">
      <ul className="flex flex-col gap-2 text-xs">
        {entries.map(([key, value]) => (
          <li
            key={key}
            className="grid grid-cols-[10rem_1fr_3rem] items-center gap-3"
          >
            <span className="truncate">{humanize(key)}</span>
            {typeof value === "number" ? (
              <>
                <span className="h-2 bg-muted">
                  <span
                    className="block h-full bg-primary"
                    style={{ width: `${(value / max) * 100}%` }}
                  />
                </span>
                <span className="text-end tabular-nums">{value}</span>
              </>
            ) : (
              <span className="col-span-2 truncate text-muted-foreground">
                {JSON.stringify(value)}
              </span>
            )}
          </li>
        ))}
      </ul>
      {numeric.length > 1 && (
        <p className="text-[11px] text-muted-foreground">
          Weights add up to {total}
          {total !== 100 && ", not 100"}.
        </p>
      )}
    </div>
  )
}

function Eligibility({
  eligibility,
}: {
  eligibility: Record<string, unknown>
}) {
  const entries = Object.entries(eligibility)
  if (entries.length === 0) {
    return (
      <p className="text-xs text-muted-foreground">
        Every pull request is eligible.
      </p>
    )
  }

  return (
    <ul className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
      {entries.map(([key, value]) => (
        <li key={key} className="flex items-center gap-2">
          {typeof value === "boolean" ? (
            value ? (
              <RiCheckLine
                className="size-4 shrink-0 text-ev-push"
                aria-label="Required"
              />
            ) : (
              <RiCloseLine
                className="size-4 shrink-0 text-muted-foreground"
                aria-label="Not allowed"
              />
            )
          ) : null}
          <span>{humanize(key)}</span>
          {typeof value !== "boolean" && (
            <span className="text-muted-foreground">
              {JSON.stringify(value)}
            </span>
          )}
        </li>
      ))}
    </ul>
  )
}
