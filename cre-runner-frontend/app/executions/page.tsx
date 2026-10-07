import type { Metadata } from "next"

import {
  attempt,
  EXECUTION_STATUSES,
  listCampaigns,
  listExecutions,
  listRepos,
  type ExecutionQuery,
  type ExecutionStatus,
} from "@/lib/api"
import { formatCount } from "@/lib/format"
import { ApiErrorState } from "@/components/api-error-state"
import { AutoRefresh } from "@/components/auto-refresh"
import { ExecutionFilters } from "@/components/execution-filters"
import { ExecutionTable } from "@/components/execution-table"
import { PageBody, PageHeader } from "@/components/page"
import { Pager } from "@/components/pager"

export const metadata: Metadata = { title: "CRE executions" }

const PAGE_SIZE = 25

function one(value: string | string[] | undefined) {
  return Array.isArray(value) ? value[0] : value
}

export default async function ExecutionsPage(props: PageProps<"/executions">) {
  const search = await props.searchParams
  const status = one(search.status)
  const pr = Number.parseInt(one(search.pr) ?? "", 10)
  const query: ExecutionQuery = {
    page: Math.max(0, Number.parseInt(one(search.page) ?? "0", 10) || 0),
    page_size: PAGE_SIZE,
    status: EXECUTION_STATUSES.includes(status as ExecutionStatus)
      ? (status as ExecutionStatus)
      : undefined,
    campaign_id: one(search.campaign_id),
    repo: one(search.repo),
    pr: Number.isFinite(pr) && pr > 0 ? pr : undefined,
  }

  const [executions, running, queued, campaigns, repos] = await Promise.all([
    attempt(listExecutions(query)),
    attempt(listExecutions({ status: "running", page_size: 1 })),
    attempt(listExecutions({ status: "queued", page_size: 1 })),
    attempt(listCampaigns({ page_size: 100 })),
    attempt(listRepos()),
  ])

  const inFlight =
    (running.ok ? running.data.pagination.total : 0) +
    (queued.ok ? queued.data.pagination.total : 0)
  // Rendered per request on the server: running durations count up to now.
  const now = new Date().toISOString()

  return (
    <PageBody>
      <AutoRefresh active={inFlight > 0} />
      <PageHeader
        title="CRE executions"
        description={
          <>
            Every CRE workflow run started by a pull request webhook, one per
            active campaign of the repository.
            {executions.ok && (
              <>
                {" "}
                {formatCount(executions.data.pagination.total)} shown
                {inFlight > 0 &&
                  `, ${inFlight} in progress (refreshing every few seconds)`}
                .
              </>
            )}
          </>
        }
      />
      <ExecutionFilters
        campaigns={
          campaigns.ok
            ? campaigns.data.data.map((c) => ({ id: c.id, name: c.name }))
            : []
        }
        repos={repos.ok ? repos.data.map((r) => r.repository_full_name) : []}
      />
      {executions.ok ? (
        <div className="flex flex-col gap-3">
          <ExecutionTable
            executions={executions.data.data}
            now={now}
            empty={
              query.status || query.campaign_id || query.repo || query.pr
                ? "No executions match these filters"
                : "No executions yet"
            }
          />
          {executions.data.data.length > 0 && (
            <Pager pagination={executions.data.pagination} noun="executions" />
          )}
        </div>
      ) : (
        <ApiErrorState
          status={executions.error.status}
          message={executions.error.message}
        />
      )}
    </PageBody>
  )
}
