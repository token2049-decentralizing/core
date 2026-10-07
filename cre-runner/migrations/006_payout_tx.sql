-- Solana transaction that paid an execution's reward (contrib_oracle on_report via the CRE forwarder).
alter table public.cre_executions
    add column if not exists payout_tx text;
