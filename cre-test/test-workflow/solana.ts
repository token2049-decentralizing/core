import type { Runtime } from "@chainlink/cre-sdk";
import type { Config } from "./types";

export type RewardDecision = {
  campaignId: string;
  repository: string;
  prNumber: number;
  contributor: string; // GitHub login for now; map to a Solana address before settlement.
  score: number;
  reward: bigint;
  evaluationHash: string;
  policyHash: string;
};

// TODO: replace with a Solana write report once the program's on_report receiver exists.
// Steps: `cre generate-bindings solana` from the program IDL, then call the generated
// writeReportFrom<Struct>() helper here.
export const submitRewardDecision = (runtime: Runtime<Config>, d: RewardDecision): void => {
  runtime.log(
    `[solana stub] reward ${d.reward} -> ${d.contributor} for ${d.repository}#${d.prNumber} (eval ${d.evaluationHash})`,
  );
};
