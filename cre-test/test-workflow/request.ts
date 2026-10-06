import type { EvaluationRequest } from "./types";

const REPO_RE = /^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/;

// Validates the raw trigger payload. Throws on bad input.
export const parseRequest = (raw: unknown): EvaluationRequest => {
  if (typeof raw !== "object" || raw === null) throw new Error("request must be an object");
  const r = raw as Record<string, unknown>;

  if (typeof r.repository !== "string" || !REPO_RE.test(r.repository)) {
    throw new Error("repository must be 'owner/repo'");
  }
  if (typeof r.pr_number !== "number" || !Number.isInteger(r.pr_number) || r.pr_number <= 0) {
    throw new Error("pr_number must be a positive integer");
  }
  if (typeof r.campaign_id !== "string" || r.campaign_id.length === 0) {
    throw new Error("campaign_id is required");
  }
  if (r.event !== "opened" && r.event !== "merged") {
    throw new Error("event must be 'opened' or 'merged'");
  }
  if (r.notes !== undefined && (typeof r.notes !== "object" || r.notes === null || Array.isArray(r.notes))) {
    throw new Error("notes must be an object");
  }

  return {
    repository: r.repository,
    pr_number: r.pr_number,
    campaign_id: r.campaign_id,
    event: r.event,
    notes: r.notes as Record<string, unknown> | undefined,
  };
};
