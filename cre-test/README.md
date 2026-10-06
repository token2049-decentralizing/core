# ContribOracle CRE

```
GitHub App ──POST /evaluate (HMAC-signed)──► runner ──warm──► reviewer ──► LLM (BytePlus ModelArk)
                                               │                  ▲
                                               ▼                  │ cache hit
                                   cre workflow simulate ── HTTP ─┘
                                     (CRE workflow, WASM) ── HTTP ──► GitHub API
```

| Path | What |
| --- | --- |
| `test-workflow/` | CRE workflow (Go): PR evidence + reviews -> score -> reward decision. See its README. |
| `cmd/runner/` | HTTP API the GitHub App calls; runs the workflow in the CRE simulator. |
| `cmd/reviewer/` | LLM PR reviewer (any OpenAI-compatible API; defaults to BytePlus ModelArk). |
| `cmd/github-app-token/` | Prints a GitHub App installation token (used by `scripts/simulate.sh`). |
| `internal/ghapp/` | GitHub App token minting + caching. |
| `Dockerfile`, `docker-compose*.yml` | One image, two services: runner + reviewer. |

**Simulation, not a DON.** The runner executes the workflow with `cre workflow simulate`: one process,
no multi-node consensus. Results are a faithful preview of the deployed workflow's logic, not
decentralized attestations. Deployed CRE is the trust layer; see [Before deploying](#before-deploying).

## Run with Docker

```bash
cp .env.example .env    # GITHUB_APP_*, RUNNER_SHARED_SECRET, REVIEWER_TOKEN, LLM_* (+ CRE_API_KEY if you have one)
docker compose up --build
curl localhost:8080/healthz   # ok
```

CRE auth, pick one:

- **API key**: set `CRE_API_KEY` in `.env` (created at app.chain.link > Account Settings; the CLI has no command for it).
- **Your `cre login` session** (local dev): leave `CRE_API_KEY` empty and not exported in your shell, run `cre login`, then

  ```bash
  docker compose -f docker-compose.yml -f docker-compose.login.yml up --build
  ```

  Mounts `~/.cre` (writable: the CLI refreshes the token) and limits the runner to 1 concurrent evaluation.
  The container gets full access to your CRE account; use it on your own machine only.

The runner logs `"cre_auth":"api_key"|"login_session"|"none"` at startup.

The App private key is mounted read-only from `GITHUB_APP_PRIVATE_KEY_PATH`.
On Linux the container user (uid 10001) must be able to read it: `chmod 644 github-app.pem`.

## LLM reviewer

Two personas, each scored 0-100 from capped category points (the model never sets the score directly):

| Endpoint | Categories (max) |
| --- | --- |
| `POST /review/code` | correctness 40, tests 25, code_quality 25, security 10 |
| `POST /review/issue` | issue_relevance 50, value 30, scope 20 |

- Fetches the PR, its diff (lockfiles/generated files dropped, capped at `MAX_DIFF_CHARS`) and the linked issue itself.
- PR text is passed as JSON-escaped data marked untrusted; the system prompt tells the model to ignore instructions in it.
- Results are cached per (persona, repo, PR, head SHA); concurrent callers share one LLM call.
- The runner warms both reviews before each simulation, so the workflow's reviewer calls are cache hits
  (CRE's HTTP timeout is 10s; an LLM review can take longer).

BytePlus ModelArk setup (`.env`):

```bash
LLM_BASE_URL=https://ark.ap-southeast.bytepluses.com/api/v3   # region of your API key
LLM_API_KEY=<ModelArk API key>
LLM_MODEL=<model ID or endpoint ID (ep-...) from the ModelArk console>
LLM_EXTRA_BODY=                 # optional, e.g. {"thinking":{"type":"disabled"}} for faster replies
LLM_JSON_MODE=false             # true only if the model supports response_format=json_object
REVIEWER_TOKEN=<openssl rand -hex 32>
```

Check the model replies before a full run (with compose up):

```bash
set -a; source .env; set +a
curl -s -X POST localhost:8090/review/issue -H "Authorization: Bearer $REVIEWER_TOKEN" \
  -d '{"repository":"token2049-decentralizing/core","pr_number":1}'
```

The first call takes as long as the model needs; a repeat for the same commit is instant (cached).

## Runner API

### `POST /evaluate`

Headers:

```
Content-Type: application/json
X-ContribOracle-Signature: sha256=<hex HMAC-SHA256(raw body, RUNNER_SHARED_SECRET)>
```

Body:

```json
{ "repository": "owner/repo", "pr_number": 1, "campaign_id": "example-oss-2026", "event": "opened",
  "head_sha": "<optional 40-char commit SHA>", "notes": {} }
```

- `event`: `opened` (preview, never pays) or `merged`.
- `head_sha`: optional but recommended (from the webhook's `pull_request.head.sha`). The evaluation fails
  if the PR head moved, so every review and score refers to one exact commit.
- `notes`: optional, ignored by scoring and hashing.

Responses:

| Status | Body |
| --- | --- |
| 200 | `{ "score": 30, "eligible": false, "reward": "0", "evaluation_hash": "0x…", "policy_hash": "0x…" }` |
| 400 | `{ "error": "…" }` invalid body |
| 401 | `{ "error": "invalid signature" }` |
| 502 | `{ "error": "…" }` workflow or reviewer failed (PR not found, head moved, LLM error, CRE auth…) |
| 503 | `{ "error": "busy, retry later" }` + `Retry-After` |
| 504 | `{ "error": "evaluation timed out" }` |

`reward` is in token base units (USDC: 6 decimals, `"445000000"` = 445 USDC).

**Settled once.** The first eligible `merged` result per (campaign, repo, PR) is recorded in a ledger
(`/data/ledger.jsonl`, Docker volume). Retries get the same result back with header
`X-ContribOracle-Replay: true` and nothing is paid twice. Concurrent duplicates wait for the first.
The Solana program must enforce the same rule on-chain.

### `GET /healthz`

`200 ok`.

## Signing a request

Node (GitHub App side):

```js
import crypto from "node:crypto";

const body = JSON.stringify({ repository, pr_number, campaign_id, event, head_sha });
const sig = "sha256=" + crypto.createHmac("sha256", process.env.RUNNER_SHARED_SECRET).update(body).digest("hex");

const res = await fetch(`${RUNNER_URL}/evaluate`, {
  method: "POST",
  headers: { "Content-Type": "application/json", "X-ContribOracle-Signature": sig },
  body, // sign and send the exact same string
});
```

curl:

```bash
BODY='{"repository":"token2049-decentralizing/core","pr_number":1,"campaign_id":"example-oss-2026","event":"opened"}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$RUNNER_SHARED_SECRET" | awk '{print $2}')"
curl -X POST localhost:8080/evaluate -H "X-ContribOracle-Signature: $SIG" -d "$BODY"
```

## How the runner works

- Validates signature and body, then queues the request (`MAX_CONCURRENCY` running, `MAX_QUEUE` waiting, 503 beyond).
- Warms both reviewer personas (in parallel), then runs
  `cre workflow simulate --target docker-simulation --wasm build/workflow.wasm`.
  The WASM is compiled once at image build, so runs are fast and can run in parallel.
- Mints/caches a read-only GitHub App token and passes it to the workflow as the `GITHUB_TOKEN` secret.
- The CLI subprocess only gets `PATH`, `HOME`, `GO*`, `CRE_*` and `*_VALUE` env vars;
  the shared secret and App key never reach it.

## Develop

```bash
go test ./...                                              # all packages
./scripts/simulate.sh test-workflow/payloads/opened.json   # local-simulation target, stub reviewers
```

`local-simulation` uses stub reviewer scores unless you put reviewer URLs in `test-workflow/config.local.json`
(e.g. `http://localhost:8090/review/code` with `go run ./cmd/reviewer` running).

## Before deploying

Simulation is enough for the hackathon. For a real deployment (needs CRE deploy access):

1. `config.production.json`: add `authorizedKeys` (EVM address the GitHub App signs gateway requests with;
   deployed HTTP triggers only accept signed JSON-RPC requests, not our HMAC) and the public reviewer URLs.
   Config validation refuses production without them.
2. `workflow.yaml`: set `deployment-registry` for `production-settings` from `cre registry list`.
3. Secrets live in Vault DON: `cre secrets create test-workflow --target production-settings`.
   A 1-hour App token cannot be stored there; use a long-lived read-only fine-grained token for `GITHUB_TOKEN`.
4. GitHub rate limits: every node fetches evidence (4+ calls per evaluation per node) with that one token.
5. `cre account link-key --target production-settings` with a funded key (not the dummy `0x…01`).
6. Simulate with the production target, then `cre workflow deploy` (deploys paused) and `cre workflow activate`.
7. Solana settlement: replace the stub in `test-workflow/solana.go` with a report write; the receiver program
   must authenticate the forwarder and reject a second payout per PR.
