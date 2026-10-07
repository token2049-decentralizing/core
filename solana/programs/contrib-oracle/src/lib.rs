//! ContribOracle campaign treasury on Solana.
//!
//! Sponsors fund a campaign vault; the ContribOracle CRE workflow writes a signed reward
//! report through the Chainlink keystone forwarder, which calls `on_report`; the program
//! checks the caller, the campaign rules and pays the contributor once per PR.
#![allow(unexpected_cfgs)]
// anchor-lang 0.31 #[program] emits AccountInfo::realloc.
#![allow(deprecated)]

use anchor_lang::prelude::*;

pub mod errors;
pub mod events;
pub mod instructions;
pub mod report;
pub mod state;

use instructions::*;
use state::CampaignStatus;

declare_id!("FSy2V61Tvm6bVHV4dGtoJS7T16eE7ZNjvGHEyT3aw6MA");

#[program]
pub mod contrib_oracle {
    use super::*;

    pub fn initialize(
        ctx: Context<Initialize>,
        forwarder_program: Pubkey,
        forwarder_state: Pubkey,
        workflow_owner: [u8; 20],
    ) -> Result<()> {
        instructions::admin::initialize(ctx, forwarder_program, forwarder_state, workflow_owner)
    }

    pub fn update_config(
        ctx: Context<UpdateConfig>,
        forwarder_program: Pubkey,
        forwarder_state: Pubkey,
        workflow_owner: [u8; 20],
    ) -> Result<()> {
        instructions::admin::update_config(ctx, forwarder_program, forwarder_state, workflow_owner)
    }

    pub fn create_campaign(
        ctx: Context<CreateCampaign>,
        campaign_id: [u8; 16],
        max_reward_per_pr: u64,
        policy_hash: [u8; 32],
    ) -> Result<()> {
        instructions::campaign::create_campaign(ctx, campaign_id, max_reward_per_pr, policy_hash)
    }

    pub fn fund_campaign(ctx: Context<FundCampaign>, amount: u64) -> Result<()> {
        instructions::campaign::fund_campaign(ctx, amount)
    }

    pub fn set_campaign_status(ctx: Context<SetCampaignStatus>, status: CampaignStatus) -> Result<()> {
        instructions::campaign::set_campaign_status(ctx, status)
    }

    pub fn close_campaign(ctx: Context<CloseCampaign>) -> Result<()> {
        instructions::campaign::close_campaign(ctx)
    }

    /// Entry point the keystone forwarder calls with a CRE report.
    pub fn on_report(ctx: Context<OnReport>, metadata: Vec<u8>, payload: Vec<u8>) -> Result<()> {
        instructions::on_report::on_report(ctx, metadata, payload)
    }
}
