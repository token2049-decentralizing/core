export const API_BASE_URL = (
  process.env.NEXT_PUBLIC_API_BASE_URL ?? "http://localhost:8080"
).replace(/\/+$/, "")

export class ApiError extends Error {
  status: number

  constructor(status: number, message: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

export type Pagination = {
  page: number
  page_size: number
  total: number
  has_more: boolean
}

export type Paginated<T> = { data: T[]; pagination: Pagination }

export type Repo = {
  repository_full_name: string
  event_count: number
  last_event_at: string
}

export type Facet = { value: string; count: number }

export type RepoFilters = {
  repository_full_name: string
  total_events: number
  events: (Facet & { actions: Facet[] })[]
  actions: Facet[]
  senders: { login: string; avatar_url: string; count: number }[]
}

export type Sender = { login: string; avatar_url: string }

export type Subject = {
  type: "pull_request" | "issue"
  number: number
  title: string
  state: string
  html_url: string
  merged?: boolean
}

export type RepoEvent = {
  id: number
  delivery_id: string
  event: string
  action: string | null
  number: number | null
  sender: Sender | null
  subject: Subject | null
  ref: string | null
  received_at: string
}

export type Delivery = {
  id: number
  delivery_id: string
  event: string
  action: string | null
  number: number | null
  hook_id: number | null
  installation_id: number | null
  repository_full_name: string | null
  sender_login: string | null
  signature_valid: boolean | null
  headers: Record<string, string>
  payload: unknown
  received_at: string
}

export const CAMPAIGN_STATUSES = ["draft", "active", "paused", "ended"] as const
export type CampaignStatus = (typeof CAMPAIGN_STATUSES)[number]

export type Campaign = {
  id: string
  name: string
  description: string | null
  sponsor: string | null
  reward_asset: "USDC" | "SOL"
  budget: number
  max_reward_per_pr: number
  min_score: number | null
  eligibility: Record<string, unknown>
  scoring: Record<string, unknown>
  status: CampaignStatus
  treasury_address: string | null
  starts_at: string | null
  ends_at: string | null
  created_at: string
  repos: string[]
}

export type CreateCampaignInput = {
  name: string
  description?: string
  sponsor?: string
  reward_asset?: "USDC" | "SOL"
  budget: string
  max_reward_per_pr: string
  min_score?: number
  eligibility?: Record<string, unknown>
  scoring?: Record<string, unknown>
  status?: "draft" | "active"
  starts_at?: string
  ends_at?: string
  repos?: string[]
}

export const EXECUTION_STATUSES = [
  "queued",
  "running",
  "completed",
  "failed",
  "skipped",
] as const
export type ExecutionStatus = (typeof EXECUTION_STATUSES)[number]

// One CRE workflow run for a (campaign, repo, PR), started by a pull_request webhook.
export type Execution = {
  id: string
  delivery_id: string
  campaign_id: string
  campaign: { id: string; name: string; reward_asset: string } | null
  repository_full_name: string
  pr_number: number
  event: "opened" | "merged"
  head_sha: string | null
  status: ExecutionStatus
  score: number | null
  eligible: boolean | null
  // Token base units as a decimal string (USDC: 6 decimals, SOL: 9).
  reward: string | null
  evaluation_hash: string | null
  policy_hash: string | null
  settled: boolean
  error: string | null
  created_at: string
  started_at: string | null
  finished_at: string | null
  // GitHub login of the PR author.
  author_login: string | null
  // Solana wallet the reward goes to; resolved for merged PRs only.
  recipient_wallet: string | null
  // Solana transaction that paid the reward on-chain.
  payout_tx: string | null
  // Execution this one is a manual rerun of.
  rerun_of: string | null
}

export type ExecutionDetail = Execution & {
  // HTTP trigger payload the runner sent to the workflow.
  request: unknown
  runner_instance: string | null
  // Score breakdown from the workflow (part of evaluation_hash); null for older runs.
  scorecard: Scorecard | null
  // Human review request, if the contributor sent one.
  appeal: Appeal | null
}

export type EvidenceCheck = {
  check: "linked_issue" | "ci" | "approval" | "tests" | "size" | (string & {})
  points: number
  max: number
  detail: string
}

export type CategoryScore = { name: string; points: number; max: number }

export type ReviewCard = {
  // Config slot; sets the weight. persona is what it judged ("code", "issue").
  role: "code_reviewer" | "llm" | (string & {})
  persona: string
  provider: string
  score: number
  categories: CategoryScore[] // Empty for a stub reviewer.
}

export type Finding = {
  persona: string
  severity: "high" | "medium" | "low"
  category: string
  file: string // Path changed in the PR, or "".
  lines: string // "88" or "88-104", or "".
  note: string
}

export type Gate = {
  gate: "merged" | "ci_passed" | "linked_issue" | "min_score" | (string & {})
  required: boolean
  passed: boolean
  detail: string
}

export type Scorecard = {
  weights: { evidenceBps: number; codeReviewerBps: number; llmBps: number }
  evidence: EvidenceCheck[]
  reviews: ReviewCard[]
  findings: Finding[]
  gates: Gate[]
}

export type Appeal = {
  id: string
  execution_id: string
  reason: string
  mentioned_login: string | null
  comment_url: string | null
  status: "open" | "resolved"
  created_at: string
}

export function isExecutionActive(execution: Execution) {
  return execution.status === "queued" || execution.status === "running"
}

// PATCH body: only the keys sent are changed, null clears an optional field and
// "repos" replaces the attached repositories.
export type UpdateCampaignInput = {
  name?: string
  description?: string | null
  sponsor?: string | null
  reward_asset?: "USDC" | "SOL"
  budget?: string
  max_reward_per_pr?: string
  min_score?: number | null
  eligibility?: Record<string, unknown> | null
  scoring?: Record<string, unknown> | null
  status?: CampaignStatus
  starts_at?: string | null
  ends_at?: string | null
  repos?: string[]
  // Vault of the on-chain campaign (Solana), recorded after it is created.
  treasury_address?: string | null
}

type Query = Record<string, string | number | undefined | null>

function buildUrl(path: string, query?: Query) {
  const url = new URL(API_BASE_URL + path)
  for (const [key, value] of Object.entries(query ?? {})) {
    if (value !== undefined && value !== null && value !== "") {
      url.searchParams.set(key, String(value))
    }
  }
  return url.toString()
}

async function request<T>(
  path: string,
  init?: RequestInit & { query?: Query }
): Promise<T> {
  let res: Response
  try {
    res = await fetch(buildUrl(path, init?.query), {
      cache: "no-store",
      ...init,
      headers: { Accept: "application/json", ...init?.headers },
    })
  } catch {
    throw new ApiError(0, `Can't reach the API at ${API_BASE_URL}`)
  }

  const body = await res.json().catch(() => null)
  if (!res.ok) {
    throw new ApiError(
      res.status,
      body?.error ?? `Request failed (${res.status})`
    )
  }
  return body as T
}

// The API takes owner/repo as two path segments; keep the slash, encode each part.
function repoPath(fullName: string) {
  return fullName.split("/").map(encodeURIComponent).join("/")
}

export async function listRepos() {
  return (await request<{ data: Repo[] }>("/api/repos")).data
}

export async function getRepoFilters(fullName: string) {
  return (
    await request<{ data: RepoFilters }>(
      `/api/repos/${repoPath(fullName)}/filters`
    )
  ).data
}

export type EventQuery = {
  page?: number
  page_size?: number
  event?: string
  action?: string
  sender?: string
  number?: number
}

export function listRepoEvents(fullName: string, query: EventQuery = {}) {
  return request<Paginated<RepoEvent>>(
    `/api/repos/${repoPath(fullName)}/events`,
    { query }
  )
}

export async function getDelivery(deliveryId: string) {
  return (
    await request<{ data: Delivery }>(
      `/api/deliveries/${encodeURIComponent(deliveryId)}`
    )
  ).data
}

export type CampaignQuery = {
  page?: number
  page_size?: number
  status?: CampaignStatus
  repo?: string
}

export function listCampaigns(query: CampaignQuery = {}) {
  return request<Paginated<Campaign>>("/api/campaigns", { query })
}

export async function getCampaign(id: string) {
  return (
    await request<{ data: Campaign }>(
      `/api/campaigns/${encodeURIComponent(id)}`
    )
  ).data
}

export async function createCampaign(input: CreateCampaignInput) {
  return (
    await request<{ data: Campaign }>("/api/campaigns", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(input),
    })
  ).data
}

export async function updateCampaign(id: string, input: UpdateCampaignInput) {
  return (
    await request<{ data: Campaign }>(
      `/api/campaigns/${encodeURIComponent(id)}`,
      {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input),
      }
    )
  ).data
}

// Fails with 409 when the campaign has CRE executions; end it instead.
export async function deleteCampaign(id: string) {
  await request<null>(`/api/campaigns/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
}

export type ExecutionQuery = {
  page?: number
  page_size?: number
  campaign_id?: string
  repo?: string
  pr?: number
  status?: ExecutionStatus
  event?: "opened" | "merged"
  // GitHub login of the PR author, case-insensitive.
  author?: string
}

export function listExecutions(query: ExecutionQuery = {}) {
  return request<Paginated<Execution>>("/api/executions", { query })
}

export async function getExecution(id: string) {
  return (
    await request<{ data: ExecutionDetail }>(
      `/api/executions/${encodeURIComponent(id)}`
    )
  ).data
}

// The wallet cre-runner pays a GitHub user's rewards to. It exists once one of their
// PRs was merged in an active campaign, even if they have never signed in.
export type ContributorWallet = {
  github_user_id: number
  github_login: string
  solana_address: string
  // Set when cre-runner pregenerated the wallet before the user signed in.
  pregenerated_at: string | null
  created_at: string
}

export async function getGitHubWallet(login: string) {
  return (
    await request<{ data: ContributorWallet }>(
      `/api/wallets/github/${encodeURIComponent(login)}`
    )
  ).data
}

// Comments on the PR and @mentions a reviewer. One request per execution (409 after that).
export async function requestReview(executionId: string, reason: string) {
  return (
    await request<{ data: Appeal }>(
      `/api/executions/${encodeURIComponent(executionId)}/appeals`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ reason }),
      }
    )
  ).data
}

export function listPRExecutions(
  campaignId: string,
  repo: string,
  number: number,
  query: { page?: number; page_size?: number } = {}
) {
  return request<Paginated<Execution>>(
    `/api/campaigns/${encodeURIComponent(campaignId)}/repos/${repoPath(repo)}/prs/${number}/executions`,
    { query }
  )
}

// Runs the execution again as a new execution (at the PR's current head); returns its id.
export async function rerunExecution(id: string) {
  return (
    await request<{ data: { id: string; rerun_of: string } }>(
      `/api/executions/${encodeURIComponent(id)}/rerun`,
      { method: "POST" }
    )
  ).data
}

export type PRCheck = {
  name: string
  status: string
  conclusion: string | null
}

export type PRLinkedIssue = {
  number: number
  url: string
  title: string | null // null: missing or not readable
  state: string | null
  is_pull_request: boolean
}

// Live pull request state from GitHub, judged like the CRE workflow judges it.
export type PRStatus = {
  repository_full_name: string
  number: number
  title: string
  html_url: string
  state: "open" | "closed"
  draft: boolean
  merged: boolean
  merged_at: string | null
  author_login: string
  head_sha: string
  base_ref: string
  // What CRE counts: "fixes/closes/resolves #N" in the PR description.
  linked_issues: PRLinkedIssue[]
  // What GitHub links (closing keywords + Development sidebar); null if unavailable.
  github_linked_issues: number[] | null
  ci_status: "PASS" | "FAIL" | "UNKNOWN"
  checks: PRCheck[]
  approvals: number
  fetched_at: string
}

export async function getPRStatus(repo: string, number: number) {
  return (
    await request<{ data: PRStatus }>(
      `/api/repos/${repoPath(repo)}/prs/${number}/status`
    )
  ).data
}

export type Result<T> = { ok: true; data: T } | { ok: false; error: ApiError }

// Lets server components render an inline error state instead of throwing,
// since thrown messages are masked in production.
export async function attempt<T>(promise: Promise<T>): Promise<Result<T>> {
  try {
    return { ok: true, data: await promise }
  } catch (error) {
    return {
      ok: false,
      error:
        error instanceof ApiError
          ? error
          : new ApiError(
              500,
              error instanceof Error ? error.message : "Unknown error"
            ),
    }
  }
}
