-- PR evaluations run through the CRE runner (cre-test/cmd/runner), one row per request.

create table if not exists public.evaluations (
    id                   bigint generated always as identity primary key,
    campaign_id          uuid        not null references public.campaigns (id) on delete cascade,
    repository_full_name text        not null check (repository_full_name ~ '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'),
    pr_number            integer     not null check (pr_number > 0),
    head_sha             text,                                  -- null for manual runs without a SHA
    event                text        not null check (event in ('opened', 'merged')),
    trigger              text        not null check (trigger in ('webhook', 'manual')),
    delivery_id          text,                                  -- X-GitHub-Delivery for webhook runs
    status               text        not null default 'pending'
                                     check (status in ('pending', 'running', 'done', 'failed')),
    score                integer     check (score between 0 and 100),
    eligible             boolean,
    reward               text,                                  -- token base units (USDC: 6 decimals)
    evaluation_hash      text,
    policy_hash          text,
    replayed             boolean     not null default false,    -- runner returned an already-settled result
    error                text,
    created_at           timestamptz not null default now(),
    updated_at           timestamptz not null default now()
);

-- GitHub redelivers webhooks: one webhook evaluation per campaign/PR/commit/event.
create unique index if not exists evaluations_webhook_dedup_idx
    on public.evaluations (campaign_id, repository_full_name, pr_number, head_sha, event)
    where trigger = 'webhook';
create index if not exists evaluations_campaign_idx on public.evaluations (campaign_id, created_at desc);
create index if not exists evaluations_repo_pr_idx  on public.evaluations (repository_full_name, pr_number, created_at desc);
create index if not exists evaluations_status_idx   on public.evaluations (status);

-- No policies: only the service role / secret key (used by cre-runner) can access this table.
alter table public.evaluations enable row level security;
