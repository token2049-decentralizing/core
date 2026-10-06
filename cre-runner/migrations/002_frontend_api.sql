-- Frontend API support: PR/issue number column, repo/facet views, campaigns.

-- PR or issue number of the event (GitHub shares one numbering space per repo).
-- Computed for existing rows as well.
alter table public.github_webhook_events
    add column if not exists number integer generated always as (
        coalesce(
            (payload -> 'pull_request' ->> 'number')::integer,
            (payload -> 'issue' ->> 'number')::integer
        )
    ) stored;

create index if not exists github_webhook_events_repo_received_idx
    on public.github_webhook_events (repository_full_name, received_at desc, id desc);
create index if not exists github_webhook_events_repo_number_idx
    on public.github_webhook_events (repository_full_name, number);

-- GET /api/repos
create or replace view public.github_webhook_repos
with (security_invoker = true) as
select repository_full_name,
       count(*)         as event_count,
       max(received_at) as last_event_at
from public.github_webhook_events
where repository_full_name is not null
group by repository_full_name;

-- GET /api/repos/:owner/:repo/filters
create or replace view public.github_webhook_repo_facets
with (security_invoker = true) as
select repository_full_name,
       event,
       action,
       sender_login,
       max(payload -> 'sender' ->> 'avatar_url') as sender_avatar_url,
       count(*)                                  as event_count
from public.github_webhook_events
where repository_full_name is not null
group by repository_full_name, event, action, sender_login;

-- POST /api/campaigns
create table if not exists public.campaigns (
    id                uuid          primary key default gen_random_uuid(),
    name              text          not null check (length(btrim(name)) > 0),
    description       text,
    sponsor           text,
    reward_asset      text          not null default 'USDC' check (reward_asset in ('USDC', 'SOL')),
    budget            numeric(20,6) not null check (budget > 0),
    max_reward_per_pr numeric(20,6) not null check (max_reward_per_pr > 0),
    min_score         integer       check (min_score between 0 and 100),
    eligibility       jsonb         not null default '{}'::jsonb,  -- e.g. {"merged": true, "ci_passed": true}
    scoring           jsonb         not null default '{}'::jsonb,  -- e.g. {"correctness": 25, "tests": 15}
    status            text          not null default 'draft'
                                    check (status in ('draft', 'active', 'paused', 'ended')),
    treasury_address  text,                                        -- Solana campaign treasury, once funded
    starts_at         timestamptz,
    ends_at           timestamptz,
    created_at        timestamptz   not null default now(),
    check (max_reward_per_pr <= budget),
    check (ends_at is null or starts_at is null or ends_at > starts_at)
);

create table if not exists public.campaign_repos (
    campaign_id          uuid        not null references public.campaigns (id) on delete cascade,
    repository_full_name text        not null
                                     check (repository_full_name ~ '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$'),
    created_at           timestamptz not null default now(),
    primary key (campaign_id, repository_full_name)
);

create index if not exists campaign_repos_repo_idx
    on public.campaign_repos (repository_full_name);

-- No policies: only the service role / secret key (used by cre-runner) can access these tables.
alter table public.campaigns      enable row level security;
alter table public.campaign_repos enable row level security;
