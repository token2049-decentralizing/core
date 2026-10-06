import type { Metadata } from "next"
import Link from "next/link"
import { notFound } from "next/navigation"
import { RiExternalLinkLine, RiTrophyLine } from "@remixicon/react"

import {
  attempt,
  getRepoFilters,
  listRepoEvents,
  type EventQuery,
} from "@/lib/api"
import { formatCount } from "@/lib/format"
import { ApiErrorState } from "@/components/api-error-state"
import { DeliverySheet } from "@/components/delivery-sheet"
import { EventFilters } from "@/components/event-filters"
import { EventTable } from "@/components/event-table"
import { PageBody, PageHeader } from "@/components/page"
import { Pager } from "@/components/pager"
import { SignalStrip } from "@/components/signal-strip"
import { Button } from "@/components/ui/button"

const PAGE_SIZE = 25
const STRIP_SIZE = 100

function one(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value
}

export async function generateMetadata(
  props: PageProps<"/repos/[owner]/[repo]">
): Promise<Metadata> {
  const { owner, repo } = await props.params
  return { title: `${decodeURIComponent(owner)}/${decodeURIComponent(repo)}` }
}

export default async function RepoPage(
  props: PageProps<"/repos/[owner]/[repo]">
) {
  const { owner, repo } = await props.params
  const search = await props.searchParams
  const fullName = `${decodeURIComponent(owner)}/${decodeURIComponent(repo)}`

  const page = Math.max(0, Number.parseInt(one(search.page) ?? "0", 10) || 0)
  const number = Number.parseInt(one(search.number) ?? "", 10)
  const query: EventQuery = {
    page,
    page_size: PAGE_SIZE,
    event: one(search.event),
    action: one(search.action),
    sender: one(search.sender),
    number: Number.isFinite(number) ? number : undefined,
  }

  const [filters, events, strip] = await Promise.all([
    attempt(getRepoFilters(fullName)),
    attempt(listRepoEvents(fullName, query)),
    attempt(listRepoEvents(fullName, { page_size: STRIP_SIZE })),
  ])

  if (!filters.ok && filters.error.status === 404) notFound()

  const isFiltered = Boolean(
    query.event || query.action || query.sender || query.number
  )

  return (
    <PageBody>
      <PageHeader
        title={fullName}
        description={
          filters.ok
            ? `${formatCount(filters.data.total_events)} webhooks from ${filters.data.senders.length} ${filters.data.senders.length === 1 ? "person" : "people"}. Select a row to inspect its payload.`
            : undefined
        }
        actions={
          <>
            <Button
              variant="outline"
              nativeButton={false}
              render={
                <Link
                  href={`/campaigns?repo=${encodeURIComponent(fullName)}`}
                />
              }
            >
              <RiTrophyLine data-icon="inline-start" />
              Campaigns
            </Button>
            <Button
              variant="outline"
              nativeButton={false}
              render={
                <a
                  href={`https://github.com/${fullName}`}
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

      {strip.ok && strip.data.data.length > 0 && (
        <section aria-label="Recent activity" className="flex flex-col gap-2">
          <p className="text-[11px] text-muted-foreground">
            Last {strip.data.data.length} webhooks, oldest to newest
          </p>
          <SignalStrip events={strip.data.data} legend />
        </section>
      )}

      <section className="flex flex-col gap-3">
        {filters.ok && <EventFilters filters={filters.data} />}
        {!events.ok ? (
          <ApiErrorState
            status={events.error.status}
            message={events.error.message}
          />
        ) : (
          <>
            <EventTable events={events.data.data} isFiltered={isFiltered} />
            {events.data.pagination.total > 0 && (
              <Pager pagination={events.data.pagination} noun="events" />
            )}
          </>
        )}
      </section>

      <DeliverySheet />
    </PageBody>
  )
}
