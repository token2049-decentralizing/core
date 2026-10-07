-- Manual reruns (POST /api/executions/:id/rerun): a rerun is a new execution that points
-- at the one it reran. Apply before deploying the runner that selects this column.
alter table public.cre_executions
    add column if not exists rerun_of uuid references public.cre_executions (id) on delete set null;

create index if not exists cre_executions_rerun_of_idx
    on public.cre_executions (rerun_of) where rerun_of is not null;
