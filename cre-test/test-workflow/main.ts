import {
  consensusIdenticalAggregation,
  consensusMedianAggregation,
  decodeJson,
  handler,
  HTTPCapability,
  HTTPClient,
  type HTTPPayload,
  Runner,
  type Runtime,
} from "@chainlink/cre-sdk";
import { fetchGitHubEvidence } from "./github";
import { hashJson } from "./hash";
import { parseRequest } from "./request";
import { callReviewer } from "./reviewers";
import { aggregateScore, evidenceScore, isEligible, rewardAmount, toBaseUnits } from "./scoring";
import { submitRewardDecision } from "./solana";
import type { Config, EvaluationResponse, GitHubEvidence, ReviewerConfig, ReviewerResult } from "./types";

export type { Config } from "./types";

const secret = (runtime: Runtime<Config>, id: string): string => {
  const value = runtime.getSecret({ id }).result().value;
  if (!value) throw new Error(`secret ${id} is empty (check .env / secrets.yaml)`);
  return value;
};

const runReviewer = (
  runtime: Runtime<Config>,
  provider: string,
  cfg: ReviewerConfig,
  evidence: GitHubEvidence,
): ReviewerResult => {
  // No URL configured: use the evidence score so local runs work without reviewer APIs.
  if (!cfg.url) return { provider: `${provider}-stub`, score: evidenceScore(evidence) };

  const apiKey = secret(runtime, cfg.secretId);
  const score = new HTTPClient()
    .sendRequest(runtime, callReviewer, consensusMedianAggregation<number>())(cfg.url, apiKey, evidence)
    .result();
  return { provider, score };
};

export const onHttpTrigger = (runtime: Runtime<Config>, payload: HTTPPayload): string => {
  const cfg = runtime.config;
  const req = parseRequest(decodeJson(payload.input));
  if (req.campaign_id !== cfg.campaign.id) throw new Error(`unknown campaign ${req.campaign_id}`);
  runtime.log(`evaluating ${req.repository}#${req.pr_number} (${req.event})`);

  const token = secret(runtime, "GITHUB_TOKEN");
  const evidenceJson = new HTTPClient()
    .sendRequest(runtime, fetchGitHubEvidence, consensusIdenticalAggregation<string>())(
      cfg.githubApiUrl,
      token,
      req.repository,
      req.pr_number,
    )
    .result();
  const evidence: GitHubEvidence = JSON.parse(evidenceJson);

  const codeReviewer = runReviewer(runtime, "code-reviewer", cfg.reviewers.codeReviewer, evidence);
  const llm = runReviewer(runtime, "llm", cfg.reviewers.llm, evidence);

  const score = aggregateScore(evidenceScore(evidence), codeReviewer, llm, cfg.campaign.weights);
  const eligible = isEligible(evidence, score, cfg.campaign);
  const reward = eligible ? toBaseUnits(rewardAmount(score, cfg.campaign), cfg.campaign.tokenDecimals) : 0n;

  const policyHash = hashJson(cfg.campaign);
  // notes are excluded on purpose: they must not change the evaluation.
  const evaluationHash = hashJson({
    repository: req.repository,
    pr_number: req.pr_number,
    campaign_id: req.campaign_id,
    event: req.event,
    evidence,
    reviewers: [codeReviewer, llm],
    score,
    eligible,
    reward: reward.toString(),
    policy_hash: policyHash,
  });

  // Only a merged, eligible PR moves money. "opened" is informational.
  if (req.event === "merged" && eligible && reward > 0n) {
    submitRewardDecision(runtime, {
      campaignId: req.campaign_id,
      repository: req.repository,
      prNumber: req.pr_number,
      contributor: evidence.author,
      score,
      reward,
      evaluationHash,
      policyHash,
    });
  }

  const res: EvaluationResponse = {
    score,
    eligible,
    reward: reward.toString(),
    evaluation_hash: evaluationHash,
    policy_hash: policyHash,
  };
  runtime.log(`result ${JSON.stringify(res)}`);
  return JSON.stringify(res);
};

export const initWorkflow = (config: Config) => [
  handler(new HTTPCapability().trigger({ authorizedKeys: config.authorizedKeys }), onHttpTrigger),
];

export async function main() {
  const runner = await Runner.newRunner<Config>();
  await runner.run(initWorkflow);
}
