-- Score breakdown per execution and contributor appeals (human review).

-- Workflow scorecard: evidence checks, reviewer rubric, findings (file + lines, no code), gates.
-- Covered by evaluation_hash.
alter table public.cre_executions
    add column if not exists scorecard jsonb;

-- One review request per execution. Posting it comments on the PR and @mentions a reviewer.
create table if not exists public.cre_execution_appeals (
    id              uuid        primary key default gen_random_uuid(),
    execution_id    uuid        not null unique references public.cre_executions (id) on delete cascade,
    reason          text        not null check (char_length(reason) between 10 and 2000),
    mentioned_login text,                                   -- reviewer @mentioned on the PR, if any
    comment_url     text,                                   -- GitHub comment with the request
    status          text        not null default 'open' check (status in ('open', 'resolved')),
    created_at      timestamptz not null default now()
);

-- No policies: only the service role / secret key (used by cre-runner) can access this table.
alter table public.cre_execution_appeals enable row level security;
