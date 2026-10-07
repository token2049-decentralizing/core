use anchor_lang::prelude::*;

#[event]
pub struct CampaignCreated {
    pub campaign: Pubkey,
    pub campaign_id: [u8; 16],
    pub sponsor: Pubkey,
    pub mint: Pubkey,
    pub max_reward_per_pr: u64,
    pub policy_hash: [u8; 32],
}

#[event]
pub struct CampaignFunded {
    pub campaign: Pubkey,
    pub funder: Pubkey,
    pub amount: u64,
}

#[event]
pub struct CampaignStatusChanged {
    pub campaign: Pubkey,
    pub status: crate::state::CampaignStatus,
}

#[event]
pub struct RewardPaid {
    pub campaign: Pubkey,
    pub payout: Pubkey,
    /// Also puts RewardReport in the IDL, so `cre generate-bindings` emits WriteReportFromRewardReport.
    pub report: crate::report::RewardReport,
}

#[event]
pub struct CampaignClosed {
    pub campaign: Pubkey,
    pub refunded: u64,
}
