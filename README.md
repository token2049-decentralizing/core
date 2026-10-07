# Kudoz

> AI can generate code. Kudoz rewards the people who generate value.

[kudoz.dev](https://kudoz.dev)

Kudoz turns merged GitHub pull requests into on-chain rewards. Sponsors fund a campaign for their repositories. Every pull request is scored by a Chainlink CRE workflow from GitHub evidence and two LLM reviews. When an eligible PR is merged, the contributor is paid in USDC or SOL from the campaign's Solana treasury. That includes contributors who have never used Kudoz: they get a wallet tied to their GitHub account and claim it by signing in with GitHub.

## Architecture

```mermaid
flowchart LR
  dev(["Contributor"])
  sponsor(["Sponsor"])

  subgraph GH["GitHub"]
    app["Kudoz GitHub App<br/>pull_request webhooks"]
    ghapi["REST + GraphQL API"]
  end

  subgraph WEB["Dashboard · kudoz.dev (Next.js)"]
    ui["Campaigns · executions<br/>live PR status · My rewards"]
    privysdk["Privy SDK<br/>GitHub sign-in · Solana wallet"]
  end

  subgraph FLY["cre-runner (Go, Fly.io)"]
    hook["POST /webhook"]
    api["/api: campaigns, executions,<br/>PR status, rerun, appeals"]
    exec["Executor<br/>queue · settle once"]
    rev["LLM reviewer<br/>/review/code · /review/issue"]
    prep["Payout prep<br/>campaign PDA · recipient ATA"]
    cli["cre workflow simulate --broadcast<br/>workflow WASM"]
  end

  db[("Supabase<br/>events · campaigns · executions<br/>wallets · appeals")]
  llm["LLM API<br/>(BytePlus ModelArk)"]
  privyapi["Privy API<br/>find / pregenerate wallets"]

  subgraph SOL["Solana devnet"]
    fwd["CRE mock forwarder"]
    prog["contrib_oracle program"]
    vault[("Campaign vault<br/>USDC / wSOL")]
    ata[("Contributor<br/>token account")]
  end

  dev -- "opens, pushes, merges PR" --> app
  app -- "webhook" --> hook
  hook --> db
  hook --> exec
  exec <--> db
  exec -- "merged: wallet for GitHub user" --> privyapi
  exec --> prep
  prep -- "read campaign, create ATA" --> prog
  exec -- "warm reviews" --> rev
  rev --> ghapi
  rev --> llm
  exec --> cli
  cli -- "evidence: PR, reviews, checks, files" --> ghapi
  cli -- "scores (cache hit)" --> rev
  cli -- "RewardReport signed with<br/>CRE_SOLANA_PRIVATE_KEY" --> fwd
  fwd -- "on_report CPI" --> prog
  prog -- "pays once per PR" --> vault
  vault --> ata
  api <--> db
  api -- "PR status, review comments" --> ghapi

  sponsor --> ui
  sponsor --> privysdk
  dev -- "claims wallet" --> privysdk
  ui --> api
  privysdk -- "create_campaign, fund<br/>(sponsor signs)" --> prog
```

## How it works

1. **Campaign.** A sponsor signs in with GitHub, creates a campaign (budget, max reward per PR, minimum score, eligibility rules, repositories) and funds its Solana vault from the dashboard.
2. **Evaluation.** Every push to an open PR runs a preview evaluation. Merging runs the paying one.
3. **Score.** The score is 40% GitHub evidence, 30% LLM code review and 30% LLM issue review.
   - Evidence: linked issue 25, CI passing 25, an approval 20, tests touched 20, size 10.
   - Code review: correctness, tests, code quality, security.
   - Issue review: does the PR resolve the linked issue, value, scope.
   - Both reviews treat PR text as untrusted input.
4. **Eligibility.** Rules the campaign can require: merged, CI passed, linked issue (`Fixes #N` in the PR description). The score must also reach the campaign's minimum.
5. **Payout.** The reward scales with the score up to the campaign's maximum per PR. The workflow writes a signed report through the CRE forwarder to the `contrib_oracle` program. The program checks the campaign rules and pays the PR author's Privy wallet. A payout record makes every PR payable only once, both on-chain and in the database.
6. **Transparency.** Each execution shows the score breakdown, evaluation hash, payout transaction, and the live PR state from GitHub. It can be re-run, or sent for human review with a comment on the PR.

## Lifecycle of a rewarded PR

```mermaid
sequenceDiagram
  autonumber
  actor S as Sponsor
  participant D as Dashboard
  participant P as Privy
  participant R as cre-runner
  participant DB as Supabase
  participant G as GitHub
  participant RV as LLM reviewer
  participant C as CRE workflow
  participant SO as Solana (contrib_oracle)
  actor A as Contributor

  rect rgb(240, 240, 255)
  note over S,SO: Campaign setup
  S->>D: Sign in with GitHub
  D->>P: Login, Solana wallet
  S->>D: Create campaign (budget, max per PR, rules, repos)
  D->>R: POST /api/campaigns
  R->>DB: Insert campaign
  S->>D: Create on Solana and deposit
  D->>P: Sign create_campaign and fund_campaign
  P->>SO: Transaction (campaign PDA, vault funded)
  D->>R: PATCH treasury_address (vault)
  end

  A->>G: Open PR "Fixes #21", push, get it merged
  G->>R: POST /webhook (pull_request closed, merged)
  R->>DB: Save webhook event
  R-->>G: 200 ack
  R->>DB: Active campaigns for repo, insert execution (queued)
  R->>DB: Not settled yet for this PR, mark running
  R->>P: Wallet for GitHub user id and login (pregenerate if new)
  P-->>R: Solana address
  R->>SO: Read campaign PDA and mint, create contributor ATA if missing
  R->>RV: Warm code and issue reviews
  RV->>G: PR, diff, linked issue
  RV-->>R: Cached scores
  R->>C: cre workflow simulate --broadcast (campaign config, recipient)
  C->>G: Evidence (PR, approvals, check runs, changed files)
  C->>RV: /review/code, /review/issue
  RV-->>C: Scores (cache hit)
  C->>C: Score, eligibility gates, reward, evaluation hash
  alt eligible and reward > 0
    C->>SO: RewardReport via forwarder, on_report
    SO->>SO: Check rules, create Payout PDA (once per PR)
    SO->>A: Transfer USDC or wSOL from vault to contributor ATA
    SO-->>C: Transaction signature
  end
  C-->>R: Score, eligible, reward, evaluation hash, payout tx
  R->>DB: Execution completed, settled, payout_tx
  D->>R: Executions, live PR status (polling)
  A->>D: Sign in with GitHub, My rewards
  D->>P: Same GitHub account opens the pregenerated wallet
```

## Repository

| Path | What |
| --- | --- |
| [`cre-runner/`](cre-runner) | Go service on Fly: webhook intake, dashboard API, CRE executions, LLM reviewer, Privy and Solana payout prep. [README](cre-runner/README.md), [API](cre-runner/API.md), [deploy guide](cre-runner/DEPLOY.md) |
| [`cre-runner/cre/`](cre-runner/cre) | The Chainlink CRE workflow (Go → WASM): evidence, scoring, reward, Solana report |
| [`cre-runner-frontend/`](cre-runner-frontend) | Next.js dashboard: repositories, campaigns, executions, wallet sign-in, campaign treasury |
| [`solana/`](solana) | Anchor program `contrib_oracle`: campaign vaults, forwarder-authenticated payouts. [README](solana/README.md) |

## Stack

Chainlink CRE · Solana (Anchor, devnet) · Privy (GitHub sign-in, embedded and pregenerated wallets) · Supabase · Go · Next.js · any OpenAI-compatible LLM (default BytePlus ModelArk).

## Getting started

Follow [`cre-runner/DEPLOY.md`](cre-runner/DEPLOY.md):

1. Supabase migrations.
2. Fly secrets.
3. GitHub App.
4. Privy.
5. Deploy the Solana program.
6. Configure the frontend.

For local development:

```bash
cd cre-runner && cp .env.example .env && go run .        # API + webhooks on :8080
cd cre-runner-frontend && pnpm install && pnpm dev       # dashboard on :3000
```

## Status

A hackathon build, not production.

- **Simulation, not a DON.** The workflow runs with `cre workflow simulate --broadcast`: real payouts on Solana devnet, but no decentralized consensus.
- **Mock forwarder.** The devnet forwarder doesn't verify DON signatures. Keep devnet campaigns small. Production needs the keystone forwarder and a pinned workflow owner.
- **Devnet only.** Reward tokens are fixed to devnet USDC (`4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU`) and wrapped SOL.
- **Unauthenticated API.** The dashboard requires a signed-in wallet to manage campaigns, but the API doesn't verify it yet.
