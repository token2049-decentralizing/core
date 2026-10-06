-- GitHub App webhook deliveries received by cre-runner.
-- https://docs.github.com/en/webhooks/webhook-events-and-payloads
create table if not exists public.github_webhook_events (
    id                   bigint generated always as identity primary key,
    delivery_id          text        not null,            -- X-GitHub-Delivery (repeats on redelivery)
    event                text        not null,            -- X-GitHub-Event
    action               text,                            -- payload.action
    hook_id              bigint,                          -- X-GitHub-Hook-ID
    installation_id      bigint,                          -- payload.installation.id
    repository_full_name text,                            -- payload.repository.full_name
    sender_login         text,                            -- payload.sender.login
    signature_valid      boolean,                         -- null when no secret is configured
    headers              jsonb       not null default '{}'::jsonb,
    payload              jsonb       not null,
    received_at          timestamptz not null default now()
);

create index if not exists github_webhook_events_delivery_idx
    on public.github_webhook_events (delivery_id);
create index if not exists github_webhook_events_event_action_idx
    on public.github_webhook_events (event, action);
create index if not exists github_webhook_events_installation_idx
    on public.github_webhook_events (installation_id);
create index if not exists github_webhook_events_repo_idx
    on public.github_webhook_events (repository_full_name);
create index if not exists github_webhook_events_received_at_idx
    on public.github_webhook_events (received_at desc);

-- No policies: only the service role key (used by cre-runner) can access this table.
alter table public.github_webhook_events enable row level security;
