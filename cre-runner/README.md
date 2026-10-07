# cre-runner

GitHub App webhook receiver, dashboard API, and ContribOracle CRE workflow executions in one service.

```
GitHub ──POST /webhook──► cre-runner ──► Supabase: github_webhook_events
                             │
                             └─ goroutine per (PR event × active campaign) ──► Supabase: cre_executions
                                   │  queued → running → completed | failed | skipped
                                   ├─ warm LLM reviews (in-process, /review/*) ──► LLM
                                   └─ cre workflow simulate (WASM) ── HTTP ──► GitHub API
                                                                   └─ HTTP ──► /review/* (cache hit)
```

| Path | What |
| --- | --- |
| `main.go`, `webhook.go` | Webhook intake (signature check, bot filter), wiring, graceful shutdown. |
| `api.go`, `cors.go` | Dashboard API incl. per-PR executions, see [API.md](API.md). |
| `execution.go` | PR webhook → executions; queue, settlement-once, status writes to `cre_executions`. |
| `policy.go` | Supabase campaign → workflow config (passed per run with `--config`). |
| `simulate.go` | Runs `cre workflow simulate` and parses its result. |
| `internal/reviewer/` | LLM PR reviewer (`POST /review/code`, `/review/issue`), served by this process. |
| `internal/ghapp/` | GitHub App token minting (read-only, per repository installation). |
| `crelogin.go`, `scripts/cre-login-env.sh` | `cre login` session as env (`CRE_LOGIN_YAML`) for Docker/Fly. |
| `cmd/github-app-token/` | Prints an App installation token (used by `cre/scripts/simulate.sh`). |
| `cre/` | CRE project: `project.yaml`, `secrets.yaml`, `test-workflow/` (separate Go module). |
| `migrations/` | Supabase schema; apply in order. |

**Simulation, not a DON.** Executions run the workflow with `cre workflow simulate`: one process, no
multi-node consensus. Results are a faithful preview of the deployed workflow's logic, not decentralized
attestations. See [Before deploying the workflow](#before-deploying-the-workflow).

## Executions

The `/evaluate` endpoint is gone. A `pull_request` webhook with a valid signature is recorded, acknowledged,
and then evaluated in the background, once per active campaign the repository is attached to:

- `opened` / `reopened` / `synchronize` / `ready_for_review` (not drafts) → `event: "opened"` (preview, never pays).
- `closed` with `merged: true` → `event: "merged"` (payout).
- The evaluation is pinned to `pull_request.head.sha`; it fails if the PR head moved.

Each execution is a `cre_executions` row whose `id` is the execution id. Status, result, and failure reason
are written as it progresses; see [API.md](API.md#cre-执行webhook-触发异步).

- **Settled once.** The first eligible `merged` result per (campaign, repo, PR) gets `settled = true`
  (unique index). Redeliveries are recorded as `skipped`. The Solana program must enforce the same rule on-chain.
- **Queue.** `MAX_CONCURRENCY` simulations run at once (1 without `CRE_WASM`); up to `MAX_QUEUE` more wait.
  Beyond that the execution fails with "runner busy". Redeliver the webhook from the App settings to retry.
- **Shutdown.** On SIGINT/SIGTERM the HTTP server stops, in-flight executions get `SHUTDOWN_GRACE_SECONDS`
  (default 240, Fly `kill_timeout` is 5m), the rest are recorded as failed. Rows left by a hard kill are
  failed on the next start of the same machine (`runner_instance`).
- The CLI subprocess only gets `PATH`, `HOME`, `GO*`, `CRE_*`, `*_VALUE`, plus per-run `GITHUB_TOKEN_VALUE`
  and `REVIEWER_TOKEN_VALUE`; the Supabase key, webhook secret, App key, and LLM key never reach it.

Without GitHub credentials (`GITHUB_APP_*` or `GITHUB_TOKEN_VALUE`) executions are disabled and webhooks
are only recorded. Without `LLM_API_KEY`/`LLM_MODEL` the workflow uses stub reviewer scores.

## Run locally

```bash
cp .env.example .env     # SUPABASE_*, GITHUB_WEBHOOK_SECRET, GITHUB_APP_*, LLM_*
cre login                # CRE auth: the CLI reads ~/.cre
go run .                 # CRE_PROJECT_DIR defaults to ./cre, target local-simulation
```

Apply `migrations/003_cre_executions.sql` in Supabase first.

## CRE auth

The CLI authenticates with your `cre login` session (`~/.cre/cre.yaml`: a 15-minute access token plus a
refresh token; the CLI refreshes and rewrites it) or with `CRE_API_KEY`, which wins when set.

- **`go run .`**: uses your `~/.cre` directly.
- **Docker / Fly**: `~/.cre` cannot be mounted on Fly (and must not be baked into the image), so pass it as env:

  ```bash
  cre login
  ./scripts/cre-login-env.sh >> .env                  # docker compose
  ./scripts/cre-login-env.sh | xargs fly secrets set  # Fly
  ```

  On start, `CRE_LOGIN_YAML` / `CRE_CONTEXT_YAML` are written to `~/.cre` **only if it has no `cre.yaml`**,
  so a session the CLI already refreshed (or your own `~/.cre`) is never overwritten. With compose, the
  `cre-session` volume keeps refreshed tokens across restarts. On Fly the filesystem resets on every
  restart, so each start begins again from the secret's refresh token. If CRE auth starts failing, run
  `cre login` and set the secret again. The session is your personal CRE account: treat the values as a password.

The startup log shows `cre_auth=api_key|login_session|login_session (seeded from CRE_LOGIN_YAML)|none`.

## Run with Docker

```bash
cp .env.example .env && ./scripts/cre-login-env.sh >> .env
docker compose up --build
```

`docker-compose.yml` mounts `./github-app.pem` read-only. On Linux the container user (uid 10001) must be
able to read it: `chmod 644 github-app.pem`.

## Deploy (Fly)

Step-by-step guide (secrets, GitHub App settings, verification, troubleshooting): [DEPLOY.md](DEPLOY.md).

The image bundles the `cre` CLI and the workflow WASM (built at image build time, so executions are fast
and can run in parallel).

```bash
fly secrets set SUPABASE_URL=... SUPABASE_SECRET_KEY=... GITHUB_WEBHOOK_SECRET=... \
  GITHUB_APP_ID=... GITHUB_APP_PRIVATE_KEY="$(cat github-app.pem)" LLM_API_KEY=... LLM_MODEL=...
./scripts/cre-login-env.sh | xargs fly secrets set
fly deploy
```

The startup log shows whether executions and the reviewer are enabled, and the CRE auth mode.

## LLM reviewer

Two personas, each scored 0-100 from capped category points (the model never sets the score directly):

| Endpoint | Categories (max) |
| --- | --- |
| `POST /review/code` | correctness 40, tests 25, code_quality 25, security 10 |
| `POST /review/issue` | issue_relevance 50, value 30, scope 20 |

- Fetches the PR, its diff (lockfiles/generated files dropped, capped at `MAX_DIFF_CHARS`) and the linked issue.
- PR text is passed as JSON-escaped data marked untrusted; the system prompt tells the model to ignore instructions in it.
- Results are cached per (persona, repo, PR, head SHA); concurrent callers share one LLM call.
- Each execution warms both reviews first, so the workflow's reviewer calls are cache hits
  (CRE's HTTP timeout is 10s; an LLM review can take longer).
- The workflow authenticates with a random per-process token. Set `REVIEWER_TOKEN` to call it by hand:

  ```bash
  curl -s -X POST localhost:8080/review/issue -H "Authorization: Bearer $REVIEWER_TOKEN" \
    -d '{"repository":"token2049-decentralizing/core","pr_number":1}'
  ```

## Develop

```bash
go test ./...                                  # runner, reviewer, ghapp
(cd cre && go test ./...)                      # workflow (separate module)
cd cre && ./scripts/simulate.sh test-workflow/payloads/opened.json   # one simulation, reads ../.env
```

`cre/test-workflow/testdata/runner-config.json` is the config the runner generates for a campaign; both
modules test against it. After changing `policy.go` run `UPDATE_GOLDEN=1 go test -run Golden .`.

## Before deploying the workflow

Simulation is enough for the hackathon. For a real CRE deployment (needs deploy access):

1. `config.production.json`: add `authorizedKeys` (EVM address the runner signs gateway requests with;
   deployed HTTP triggers only accept signed JSON-RPC requests) and public reviewer URLs.
   Config validation refuses production without them.
2. `workflow.yaml`: set `deployment-registry` for `production-settings` from `cre registry list`.
3. Secrets live in Vault DON: `cre secrets create test-workflow --target production-settings`.
   A 1-hour App token cannot be stored there; use a long-lived read-only fine-grained token for `GITHUB_TOKEN`.
4. GitHub rate limits: every node fetches evidence (4+ calls per evaluation per node) with that one token.
5. `cre account link-key --target production-settings` with a funded key (not the dummy `0x…01`).
6. Simulate with the production target, then `cre workflow deploy` (deploys paused) and `cre workflow activate`.
7. Solana settlement: replace the stub in `cre/test-workflow/solana.go` with a report write; the receiver
   program must authenticate the forwarder and reject a second payout per PR.
