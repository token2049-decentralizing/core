import { type HTTPSendRequester, json, ok } from "@chainlink/cre-sdk";
import type { CiStatus, GitHubEvidence } from "./types";

// "closes #12", "fixes #3", "resolved #7" etc. in the PR body.
const LINKED_ISSUE_RE = /\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\s+#(\d+)/gi;
const TEST_FILE_RE = /(^|\/)(tests?|__tests__|spec)(\/|$)|\.(test|spec)\.[a-z]+$|_test\.[a-z]+$/i;

export const parseLinkedIssues = (body: string): number[] => {
  const ids = new Set<number>();
  for (const m of body.matchAll(LINKED_ISSUE_RE)) ids.add(Number(m[1]));
  return [...ids].sort((a, b) => a - b);
};

export const ciStatusFrom = (checkRuns: { status: string; conclusion: string | null }[]): CiStatus => {
  if (checkRuns.length === 0) return "UNKNOWN";
  if (checkRuns.some((c) => c.status !== "completed")) return "UNKNOWN";
  const passing = ["success", "neutral", "skipped"];
  return checkRuns.every((c) => passing.includes(c.conclusion ?? "")) ? "PASS" : "FAIL";
};

// Runs on each node. Returns JSON string so nodes can reach identical consensus.
export const fetchGitHubEvidence = (
  sendRequester: HTTPSendRequester,
  apiUrl: string,
  token: string,
  repository: string,
  prNumber: number,
): string => {
  const get = (path: string): any => {
    const res = sendRequester
      .sendRequest({
        url: `${apiUrl}${path}`,
        method: "GET",
        headers: {
          Accept: "application/vnd.github+json",
          Authorization: `Bearer ${token}`,
          "User-Agent": "contriboracle",
          "X-GitHub-Api-Version": "2022-11-28",
        },
      })
      .result();
    if (!ok(res)) throw new Error(`GitHub ${path} -> HTTP ${res.statusCode}`);
    return json(res);
  };

  const base = `/repos/${repository}/pulls/${prNumber}`;
  const pr = get(base);
  const reviews: any[] = get(`${base}/reviews?per_page=100`);
  // TODO: paginate if PRs exceed 100 files.
  const files: any[] = get(`${base}/files?per_page=100`);
  const checks = get(`/repos/${repository}/commits/${pr.head.sha}/check-runs?per_page=100`);

  const approvers = new Set(reviews.filter((r) => r.state === "APPROVED").map((r) => r.user?.login));
  const body: string = pr.body ?? "";

  const evidence: GitHubEvidence = {
    repository,
    pullRequest: prNumber,
    author: pr.user?.login ?? "",
    title: pr.title ?? "",
    body,
    baseSha: pr.base.sha,
    headSha: pr.head.sha,
    additions: pr.additions,
    deletions: pr.deletions,
    changedFiles: pr.changed_files,
    testsTouched: files.some((f) => TEST_FILE_RE.test(f.filename)),
    linkedIssues: parseLinkedIssues(body),
    ciStatus: ciStatusFrom(checks.check_runs ?? []),
    reviewApprovals: approvers.size,
    merged: pr.merged === true,
    labels: (pr.labels ?? []).map((l: any) => l.name),
  };
  return JSON.stringify(evidence);
};
