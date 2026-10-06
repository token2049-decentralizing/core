// Contract between the GitHub App and this workflow.

export type EvaluationEvent = "opened" | "merged";

export type EvaluationRequest = {
  repository: string; // "owner/repo"
  pr_number: number;
  campaign_id: string;
  event: EvaluationEvent;
  notes?: Record<string, unknown>; // Free-form, untrusted. Never affects score or reward.
};

export type EvaluationResponse = {
  score: number;
  eligible: boolean;
  reward: string; // Token base units, as a string (bigint-safe).
  evaluation_hash: string;
  policy_hash: string;
};

export type RewardModel =
  | { model: "fixed"; minScore: number; amount: number }
  | { model: "score_based"; max: number }
  | { model: "tiered"; tiers: { minScore: number; amount: number }[] };

export type CampaignPolicy = {
  id: string;
  tokenDecimals: number;
  eligibility: {
    requireMerged: boolean;
    requireCiPassed: boolean;
    requireLinkedIssue: boolean;
    minScore: number;
  };
  weights: { evidence: number; codeReviewer: number; llm: number }; // Must sum to 1.
  reward: RewardModel;
};

export type ReviewerConfig = {
  url: string; // Empty = stub score (for local testing).
  secretId: string;
};

export type Config = {
  authorizedKeys: { type: "KEY_TYPE_ECDSA_EVM"; publicKey: string }[];
  githubApiUrl: string;
  campaign: CampaignPolicy;
  reviewers: { codeReviewer: ReviewerConfig; llm: ReviewerConfig };
};

export type CiStatus = "PASS" | "FAIL" | "UNKNOWN";

export type GitHubEvidence = {
  repository: string;
  pullRequest: number;
  author: string;
  title: string;
  body: string;
  baseSha: string;
  headSha: string;
  additions: number;
  deletions: number;
  changedFiles: number;
  testsTouched: boolean;
  linkedIssues: number[];
  ciStatus: CiStatus;
  reviewApprovals: number;
  merged: boolean;
  labels: string[];
};

export type ReviewerResult = {
  provider: string;
  score: number; // 0-100
};
