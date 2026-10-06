import type { CampaignPolicy, GitHubEvidence, ReviewerResult } from "./types";

// Deterministic 0-100 score from GitHub evidence only.
export const evidenceScore = (e: GitHubEvidence): number => {
  let s = 0;
  if (e.linkedIssues.length > 0) s += 25;
  if (e.ciStatus === "PASS") s += 25;
  if (e.reviewApprovals > 0) s += 20;
  if (e.testsTouched) s += 20;

  // Huge diffs are harder to review; penalise them.
  const size = e.additions + e.deletions;
  if (size <= 1000) s += 10;
  else if (size <= 3000) s += 5;

  return s;
};

const clamp = (n: number) => Math.max(0, Math.min(100, n));

// Weighted final score, rounded to an integer.
export const aggregateScore = (
  evidence: number,
  codeReviewer: ReviewerResult,
  llm: ReviewerResult,
  weights: CampaignPolicy["weights"],
): number =>
  Math.round(
    clamp(evidence) * weights.evidence +
      clamp(codeReviewer.score) * weights.codeReviewer +
      clamp(llm.score) * weights.llm,
  );

export const isEligible = (e: GitHubEvidence, score: number, p: CampaignPolicy): boolean => {
  const el = p.eligibility;
  if (el.requireMerged && !e.merged) return false;
  if (el.requireCiPassed && e.ciStatus !== "PASS") return false;
  if (el.requireLinkedIssue && e.linkedIssues.length === 0) return false;
  return score >= el.minScore;
};

// Reward in whole tokens (e.g. USDC).
export const rewardAmount = (score: number, p: CampaignPolicy): number => {
  const r = p.reward;
  switch (r.model) {
    case "fixed":
      return score >= r.minScore ? r.amount : 0;
    case "score_based":
      return (r.max * score) / 100;
    case "tiered": {
      const tier = [...r.tiers].sort((a, b) => b.minScore - a.minScore).find((t) => score >= t.minScore);
      return tier ? tier.amount : 0;
    }
  }
};

// Whole tokens -> base units (rounded to token decimals).
export const toBaseUnits = (amount: number, decimals: number): bigint => {
  const [whole, frac = ""] = amount.toFixed(decimals).split(".");
  return BigInt(whole + frac.padEnd(decimals, "0").slice(0, decimals));
};
