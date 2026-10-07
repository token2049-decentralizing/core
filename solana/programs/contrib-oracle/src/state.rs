use anchor_lang::prelude::*;

pub const CONFIG_SEED: &[u8] = b"config";
pub const CAMPAIGN_SEED: &[u8] = b"campaign";
pub const VAULT_SEED: &[u8] = b"vault";
pub const PAYOUT_SEED: &[u8] = b"payout";
pub const RENT_PAYER_SEED: &[u8] = b"rent_payer";
/// Seed the keystone forwarder uses for its per-receiver signer PDA.
pub const FORWARDER_AUTHORITY_SEED: &[u8] = b"forwarder";

/// Program-wide settings. One per deployment.
#[account]
#[derive(InitSpace)]
pub struct Config {
    pub admin: Pubkey,
    /// Keystone forwarder (or the mock forwarder used by `cre workflow simulate`).
    pub forwarder_program: Pubkey,
    pub forwarder_state: Pubkey,
    /// Only reports from this workflow owner pay out. All zeros = any workflow.
    pub workflow_owner: [u8; 20],
    pub bump: u8,
}

#[derive(AnchorSerialize, AnchorDeserialize, Clone, Copy, PartialEq, Eq, InitSpace, Debug)]
pub enum CampaignStatus {
    Active,
    Paused,
    Closed,
}

/// A sponsor-funded campaign. Mirrors a cre-runner campaign (same UUID).
#[account]
#[derive(InitSpace)]
pub struct Campaign {
    pub campaign_id: [u8; 16],
    pub sponsor: Pubkey,
    pub mint: Pubkey,
    pub vault: Pubkey,
    /// Cap per PR, in token base units.
    pub max_reward_per_pr: u64,
    /// Recorded at creation but no longer enforced: reports don't carry a policy hash
    /// (CRE report size limit); evaluation_hash commits to the policy instead.
    /// Kept so existing campaign accounts keep their layout.
    pub policy_hash: [u8; 32],
    pub status: CampaignStatus,
    pub total_paid: u64,
    pub payout_count: u64,
    pub bump: u8,
    pub vault_bump: u8,
}

/// One per paid contribution; its existence is what prevents paying a PR twice.
#[account]
#[derive(InitSpace)]
pub struct Payout {
    pub campaign: Pubkey,
    pub contribution_id: [u8; 32],
    pub recipient: Pubkey,
    pub amount: u64,
    pub evaluation_hash: [u8; 32],
    pub workflow_execution_report_id: [u8; 2],
    pub paid_at: i64,
}
