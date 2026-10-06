# ContribOracle CRE workflow (Go)

HTTP-triggered workflow: GitHub PR -> evidence + reviewers -> score -> reward decision.

| File | Purpose |
| --- | --- |
| `main.go` | WASM entry point |
| `workflow.go` | Trigger handler and workflow wiring |
| `types.go` | Request/response contract and config types |
| `request.go` | Input validation |
| `github.go` | GitHub evidence fetch |
| `reviewers.go` | Code reviewer / LLM calls |
| `scoring.go` | Score, eligibility, reward |
| `hash.go` | Canonical JSON + SHA-256 |
| `solana.go` | Settlement (stub) |

Requires Go 1.25.3+ (`go.mod` is in `cre-test/`).

## Concurrency

The CRE runtime is single-threaded and must not be used from goroutines.
Run requests in parallel by starting several calls, then awaiting them:

```go
a, b := sr.SendRequest(reqA), sr.SendRequest(reqB) // both in flight
resA, _ := a.Await()
resB, _ := b.Await()
```

`github.go` does this for reviews/files/check-runs, `reviewers.go` for both reviewers.
Use goroutines in services outside the workflow (runner, reviewer service, load tests).

## Request

```json
{ "repository": "owner/repo", "pr_number": 1, "campaign_id": "example-oss-2026", "event": "opened", "notes": {} }
```

`notes` is optional and never affects the score, reward or hash.

## Response

```json
{ "score": 90, "eligible": true, "reward": "450000000", "evaluation_hash": "0x...", "policy_hash": "0x..." }
```

`reward` is in token base units (USDC = 6 decimals).
Hashes are SHA-256 of canonical JSON (sorted keys), so any language can recompute them.

## Setup

```bash
cd ..                       # cre-test/
cp .env.example .env        # fill in GitHub App vars
go mod download
```

Reviewer `url` empty in `config.*.json` = stub score, so only GitHub access is required to start.

## Test

```bash
go test ./...                                     # unit + workflow tests, no network
GOOS=wasip1 GOARCH=wasm go build -o /dev/null ./test-workflow  # same target CRE compiles to
```

## Simulate (from `cre-test/`)

`scripts/simulate.sh` mints a 1-hour, read-only GitHub App token (`cmd/github-app-token`) and runs the simulation:

```bash
./scripts/simulate.sh test-workflow/payloads/opened.json
```
