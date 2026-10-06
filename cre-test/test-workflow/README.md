# ContribOracle CRE workflow

HTTP-triggered workflow: GitHub PR -> evidence + reviewers -> score -> reward decision.

| File | Purpose |
| --- | --- |
| `main.ts` | Trigger handler and workflow wiring |
| `types.ts` | Request/response contract and config types |
| `request.ts` | Input validation |
| `github.ts` | GitHub evidence fetch |
| `reviewers.ts` | Code reviewer / LLM calls |
| `scoring.ts` | Score, eligibility, reward |
| `hash.ts` | Canonical JSON + SHA-256 |
| `solana.ts` | Settlement (stub) |

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

## Setup

```bash
cp ../.env.example ../.env   # fill in GitHub App vars (or GITHUB_TOKEN_VALUE)
bun install
```

Reviewer `url` empty in `config.*.json` = stub score, so only GitHub access is required to start.

### GitHub App token

`../scripts/github-app-token.ts` mints a 1-hour, read-only installation token from the App ID + private key.
`../scripts/simulate.sh` mints one and runs the simulation with it:

```bash
cd ..   # cre-test/
./scripts/simulate.sh test-workflow/payloads/opened.json
```

## Test

```bash
bun test              # unit tests, no network
bun run typecheck
```

## Simulate (from `cre-test/`)

Flags may differ by CLI version; check `cre workflow simulate --help`. Without them the CLI prompts for trigger and JSON input.

```bash
cre workflow simulate test-workflow --target staging-settings \
  --non-interactive --trigger-index 0 \
  --http-payload "$(cat test-workflow/payloads/opened.json)"
```
