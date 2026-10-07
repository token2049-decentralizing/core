use anchor_lang::prelude::*;

#[error_code]
pub enum OracleError {
    #[msg("Caller is not the configured forwarder")]
    UnauthorizedForwarder,
    #[msg("Report comes from a workflow owner that is not allowed")]
    UnauthorizedWorkflow,
    #[msg("Report metadata must be 64 bytes")]
    InvalidMetadata,
    #[msg("Report payload is not a valid RewardReport")]
    InvalidPayload,
    #[msg("Campaign account does not match the report's campaign_id")]
    CampaignMismatch,
    #[msg("Campaign is not active")]
    CampaignNotActive,
    #[msg("Report was produced under a different campaign policy")]
    PolicyMismatch,
    #[msg("Reward must be greater than zero")]
    ZeroReward,
    #[msg("Reward exceeds the campaign's max_reward_per_pr")]
    RewardAboveCap,
    #[msg("Campaign vault cannot cover this reward")]
    InsufficientFunds,
    #[msg("Payout account does not match the expected PDA")]
    PayoutMismatch,
    #[msg("This contribution has already been paid")]
    AlreadyPaid,
    #[msg("Recipient token account is not owned by the report's recipient")]
    RecipientMismatch,
    #[msg("Only the campaign sponsor can do this")]
    NotSponsor,
    #[msg("Only the admin can do this")]
    NotAdmin,
    #[msg("Campaign is closed")]
    CampaignClosed,
    #[msg("Use close_campaign to close a campaign")]
    UseCloseCampaign,
    #[msg("Amount must be greater than zero")]
    ZeroAmount,
    #[msg("Arithmetic overflow")]
    Overflow,
}
