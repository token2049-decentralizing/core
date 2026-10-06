# ContribOracle CRE workflow (Go)

HTTP-triggered workflow: GitHub PR -> evidence + reviews -> score -> reward decision.

| File | Purpose |
| --- | --- |
| `main.go` | WASM entry point |
| `workflow.go` | Trigger handler, config validation, workflow wiring |
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
| `local-simulation` | `config.local.json` | `scripts/simulate.sh` on your machine; stub reviewers |
| `docker-simulation` | `config.docker.json` | runner image; reviewers at `http://reviewer:8090` |
| `production-settings` | `config.production.json` | deployment; refused until `authorizedKeys` and reviewer URLs are set |

## Config

- `mode`: `simulation` (allows empty `authorizedKeys`, stub reviewers) or `production`.
- `campaign.weights`: basis points, must sum to 10000.
- `campaign.reward`: amounts are decimal strings in whole tokens (`"500"`, `"0.25"`), converted to base units
  with `tokenDecimals`. `score_based` floors, so a reward never exceeds the policy.
- A reviewer with an empty `url` scores with the evidence score (stub, simulation only).

`policy_hash` is the SHA-256 of the canonical `campaign` JSON: change the policy, change the hash.

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
{ "repository": "owner/repo", "pr_number": 1, "campaign_id": "example-oss-2026", "event": "opened",
  "head_sha": "<optional>", "notes": {} }
```

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
