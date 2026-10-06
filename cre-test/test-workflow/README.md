# ContribOracle CRE workflow (Go)

HTTP-triggered workflow: GitHub PR -> evidence + reviews -> score -> reward decision.

| File | Purpose |
| --- | --- |
| `main.go` | WASM entry point |
| `workflow.go` | Trigger handler, config validation, workflow wiring |
| `campaign.go` | Campaign policy from cre-runner (`GET /api/campaigns/{id}`) |
| `types.go` | Request/response contract and config types |
| `request.go` | Input validation |
| `github.go` | GitHub evidence fetch (REST + GraphQL file paths) |
| `reviewers.go` | Code reviewer / LLM calls (median across nodes) |
| `scoring.go` | Score, eligibility, reward (integer math) |
| `hash.go` | Canonical JSON + SHA-256 |
| `solana.go` | Settlement (stub) |

Requires Go 1.25.3+ (`go.mod` is in `cre-test/`).

## Targets

| Target | Config | Use |
| --- | --- | --- |
| `local-simulation` | `config.local.json` | `scripts/simulate.sh`; static offline campaign, stub reviewers |
| `docker-simulation` | `config.docker.json` | runner image; campaigns from cre-runner, reviewers at `http://reviewer:8090` |
| `production-settings` | `config.production.json` | deployment; refused until `authorizedKeys` and reviewer URLs are set |

## Campaigns

With `campaignApiUrl` set (docker, production), each evaluation fetches `GET {campaignApiUrl}/api/campaigns/{campaign_id}`
from cre-runner on every node (identical consensus), then requires:

- `status` is `active`, the PR's repository is in `repos`, and DON time is within `starts_at`/`ends_at`.
- Mapping: `eligibility.merged|ci_passed|linked_issue` -> eligibility rules, `min_score`, `max_reward_per_pr`
  (score-based: `max * score / 100`, floored), `reward_asset` USDC (6 decimals) or SOL (9).
- `weights` (evidence / code reviewer / LLM, basis points) come from the workflow config, not the campaign.

`policy_hash` is the SHA-256 of the canonical applied policy (campaign rules + weights).

With `campaignApiUrl` empty, the static `campaign` in config is used (simulation only, for offline runs and tests).

## Config

- `mode`: `simulation` (allows empty `authorizedKeys`, stub reviewers, static campaign) or `production`.
- `weights`: basis points, must sum to 10000.
- Static `campaign.reward` amounts are decimal strings in whole tokens (`"500"`, `"0.25"`).
- A reviewer with an empty `url` scores with the evidence score (stub, simulation only).

## Determinism

- GitHub evidence is fetched on each node and must match exactly (identical consensus); labels are sorted,
  and passing `head_sha` pins the evaluation to one commit.
- File paths come from GraphQL (paths only): the REST files endpoint returns every patch and can exceed the
  CRE HTTP response limit (250 KB in simulation).
- Reviewer scores use median consensus per field. Reviewer POSTs are never cached by CRE and time out at 9s
  (the runner pre-warms the reviewer cache).

## Concurrency

The CRE runtime is single-threaded and must not be used from goroutines.
Run requests in parallel by starting several calls, then awaiting them:

```go
a, b := sr.SendRequest(reqA), sr.SendRequest(reqB) // both in flight
resA, _ := a.Await()
resB, _ := b.Await()
```

`github.go` does this for reviews/check-runs/files, `reviewers.go` for both reviewers.
Use goroutines in services outside the workflow (runner, reviewer, load tests).

## Request

```json
{ "repository": "owner/repo", "pr_number": 1, "campaign_id": "<cre-runner campaign UUID>", "event": "opened",
  "head_sha": "<optional>", "notes": {} }
```

`campaign_id` is a cre-runner campaign UUID (`example-oss-2026` for the static offline campaign).
`notes` is optional and never affects the score, reward or hash.

## Response

```json
{ "score": 90, "eligible": true, "reward": "450000000", "evaluation_hash": "0x...", "policy_hash": "0x..." }
```

`reward` is in token base units (USDC = 6 decimals).

## Test

```bash
go test ./...                                                   # from cre-test/, no network
GOOS=wasip1 GOARCH=wasm go build -o /dev/null ./test-workflow   # same target CRE compiles to
```

## Simulate (from `cre-test/`)

`scripts/simulate.sh` mints a 1-hour, read-only GitHub App token (`cmd/github-app-token`) and runs the
simulation on `local-simulation` (override with `CRE_TARGET=...`):

```bash
./scripts/simulate.sh test-workflow/payloads/opened.json
```
