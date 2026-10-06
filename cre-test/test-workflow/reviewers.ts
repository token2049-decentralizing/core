import { bytesToBase64, type HTTPSendRequester, json, ok } from "@chainlink/cre-sdk";
import type { GitHubEvidence } from "./types";

// Body sent to every reviewer. PR text is passed as data, never as instructions.
export const reviewerRequestBody = (e: GitHubEvidence) => ({
  repository: e.repository,
  pr_number: e.pullRequest,
  base_sha: e.baseSha,
  head_sha: e.headSha,
  linked_issues: e.linkedIssues,
  untrusted: { title: e.title, body: e.body },
});

// Runs on each node. Expects the reviewer to reply { "score": 0-100 }.
// TODO: adapt request/response to the real reviewer and LLM APIs.
export const callReviewer = (
  sendRequester: HTTPSendRequester,
  url: string,
  apiKey: string,
  evidence: GitHubEvidence,
): number => {
  const res = sendRequester
    .sendRequest({
      url,
      method: "POST",
      headers: { "Content-Type": "application/json", Authorization: `Bearer ${apiKey}` },
      body: bytesToBase64(new TextEncoder().encode(JSON.stringify(reviewerRequestBody(evidence)))),
    })
    .result();
  if (!ok(res)) throw new Error(`reviewer ${url} -> HTTP ${res.statusCode}`);

  const score = Number((json(res) as { score?: unknown }).score);
  if (!Number.isFinite(score)) throw new Error(`reviewer ${url} returned no numeric score`);
  return Math.max(0, Math.min(100, score));
};
