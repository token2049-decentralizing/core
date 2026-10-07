-- Payout recipients: each PR author's Privy Solana wallet. For contributors who never signed
-- in, cre-runner pregenerates the wallet from their GitHub account; signing in with GitHub
-- later gives them control of the same wallet.

alter table public.cre_executions
    add column if not exists author_login     text,    -- pull_request.user.login
    add column if not exists author_github_id bigint,  -- pull_request.user.id
    add column if not exists recipient_wallet text;    -- Solana address the reward goes to (merged only)

create index if not exists cre_executions_author_idx
    on public.cre_executions (author_login, created_at desc);

create table if not exists public.contributor_wallets (
    github_user_id  bigint      primary key,          -- numeric GitHub id (logins can be renamed)
    github_login    text        not null,
    privy_user_id   text        not null,
    solana_address  text        not null,
    pregenerated_at timestamptz,                      -- set when cre-runner created the Privy user
    created_at      timestamptz not null default now(),
    updated_at      timestamptz not null default now()
);

create index if not exists contributor_wallets_login_idx
    on public.contributor_wallets (lower(github_login));

-- No policies: only the service role / secret key (used by cre-runner) can access this table.
alter table public.contributor_wallets enable row level security;
