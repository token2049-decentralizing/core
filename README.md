# Kudoz

**Reward contributions that matter.**

Kudoz rewards meaningful open-source contributions. Sponsors fund campaigns, contributors solve GitHub issues, and qualifying merged pull requests earn rewards on Solana.

**Start with GitHub.** Contributors sign in with their GitHub account. Kudoz creates an embedded wallet through Privy when needed, with no separate wallet setup.

## Meet Kudoz

| Project Film | Product Demo |
| --- | --- |
| [![Play the Kudoz project film](https://i.ytimg.com/vi/YzVUoyMnH0U/hqdefault.jpg)](https://youtu.be/YzVUoyMnH0U) | [![Play the Kudoz product demo](https://i.ytimg.com/vi/KA7dAEH4m4w/hqdefault.jpg)](https://youtu.be/KA7dAEH4m4w) |
| The idea behind Kudoz. | See Kudoz in action. |
| [▶ Watch the film](https://youtu.be/YzVUoyMnH0U) | [▶ Watch the demo](https://youtu.be/KA7dAEH4m4w) |

Select a thumbnail to watch on YouTube.

## How it works

1. **Fund a campaign.** Sponsors choose a repository, define reward rules, and deposit tokens into a campaign vault.
2. **Evaluate contributions.** GitHub events trigger a Chainlink CRE workflow that combines PR evidence with AI reviews of code quality and issue relevance.
3. **Pay for qualifying work.** Open PRs receive an evaluation preview. Eligible merged PRs trigger a payout to the contributor’s GitHub-linked wallet.

## Architecture

![Kudoz architecture: GitHub events and dashboard settings feed the runner; CRE combines evidence, reviews, and campaign rules; eligible merged PRs produce a reward report that pays from a Solana campaign vault.](docs/architecture.svg)

The **runner** connects GitHub, the dashboard, and campaign data in Supabase. **CRE** combines evidence and reviews into a reward decision. **Privy** handles contributor wallets, and the **Solana program** validates payouts and transfers tokens from the campaign vault.

## Why Kudoz

- **Quality guides rewards.** AI reviews inform the score. Campaign rules set eligibility requirements such as passing CI, a linked issue, and a minimum score. Payment requires a merge.
- **Payouts have clear limits.** The Solana program checks the reward cap, vault balance, and recipient, and allows one payout per PR per campaign.
- **Decisions are traceable.** Scorecards explain each evaluation. An evaluation hash links the decision to its on-chain payout record.

**Prototype status:** Wallet creation and payouts are implemented. The runner uses `cre workflow simulate` and can broadcast to Solana devnet when configured. The devnet mock forwarder does not verify reports; this prototype does not provide decentralized CRE consensus.

## Code & setup

| Component | Location |
| --- | --- |
| Dashboard · Next.js | [cre-runner-frontend](cre-runner-frontend/) |
| Runner & CRE workflow · Go | [Setup](cre-runner/README.md) · [Deployment](cre-runner/DEPLOY.md) |
| Campaign vault & payouts · Solana / Anchor | [solana](solana/README.md) |
