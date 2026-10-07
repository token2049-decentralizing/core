use anchor_lang::prelude::*;
use anchor_spl::token_interface::{
    close_account, transfer_checked, CloseAccount, Mint, TokenAccount, TokenInterface, TransferChecked,
};

use crate::errors::OracleError;
use crate::events::*;
use crate::state::*;

#[derive(Accounts)]
#[instruction(campaign_id: [u8; 16])]
pub struct CreateCampaign<'info> {
    #[account(mut)]
    pub sponsor: Signer<'info>,
    #[account(
        init,
        payer = sponsor,
        space = 8 + Campaign::INIT_SPACE,
        seeds = [CAMPAIGN_SEED, campaign_id.as_ref()],
        bump
    )]
    pub campaign: Account<'info, Campaign>,
    pub mint: InterfaceAccount<'info, Mint>,
    /// Program-owned vault: only this program (signing as the campaign) can move funds.
    #[account(
        init,
        payer = sponsor,
        seeds = [VAULT_SEED, campaign.key().as_ref()],
        bump,
        token::mint = mint,
        token::authority = campaign,
        token::token_program = token_program
    )]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    pub token_program: Interface<'info, TokenInterface>,
    pub system_program: Program<'info, System>,
}

pub fn create_campaign(
    ctx: Context<CreateCampaign>,
    campaign_id: [u8; 16],
    max_reward_per_pr: u64,
    policy_hash: [u8; 32],
) -> Result<()> {
    require!(max_reward_per_pr > 0, OracleError::ZeroAmount);
    let campaign = &mut ctx.accounts.campaign;
    campaign.set_inner(Campaign {
        campaign_id,
        sponsor: ctx.accounts.sponsor.key(),
        mint: ctx.accounts.mint.key(),
        vault: ctx.accounts.vault.key(),
        max_reward_per_pr,
        policy_hash,
        status: CampaignStatus::Active,
        total_paid: 0,
        payout_count: 0,
        bump: ctx.bumps.campaign,
        vault_bump: ctx.bumps.vault,
    });
    emit!(CampaignCreated {
        campaign: campaign.key(),
        campaign_id,
        sponsor: campaign.sponsor,
        mint: campaign.mint,
        max_reward_per_pr,
        policy_hash,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct FundCampaign<'info> {
    pub funder: Signer<'info>,
    #[account(constraint = campaign.status != CampaignStatus::Closed @ OracleError::CampaignClosed)]
    pub campaign: Account<'info, Campaign>,
    #[account(address = campaign.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(mut, address = campaign.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, token::mint = mint, token::authority = funder, token::token_program = token_program)]
    pub funder_token: InterfaceAccount<'info, TokenAccount>,
    pub token_program: Interface<'info, TokenInterface>,
}

/// Anyone can top up a campaign (usually the sponsor).
pub fn fund_campaign(ctx: Context<FundCampaign>, amount: u64) -> Result<()> {
    require!(amount > 0, OracleError::ZeroAmount);
    transfer_checked(
        CpiContext::new(
            ctx.accounts.token_program.to_account_info(),
            TransferChecked {
                from: ctx.accounts.funder_token.to_account_info(),
                mint: ctx.accounts.mint.to_account_info(),
                to: ctx.accounts.vault.to_account_info(),
                authority: ctx.accounts.funder.to_account_info(),
            },
        ),
        amount,
        ctx.accounts.mint.decimals,
    )?;
    emit!(CampaignFunded {
        campaign: ctx.accounts.campaign.key(),
        funder: ctx.accounts.funder.key(),
        amount,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct SetCampaignStatus<'info> {
    pub sponsor: Signer<'info>,
    #[account(
        mut,
        has_one = sponsor @ OracleError::NotSponsor,
        constraint = campaign.status != CampaignStatus::Closed @ OracleError::CampaignClosed
    )]
    pub campaign: Account<'info, Campaign>,
}

/// Pause or resume payouts.
pub fn set_campaign_status(ctx: Context<SetCampaignStatus>, status: CampaignStatus) -> Result<()> {
    require!(status != CampaignStatus::Closed, OracleError::UseCloseCampaign);
    ctx.accounts.campaign.status = status;
    emit!(CampaignStatusChanged { campaign: ctx.accounts.campaign.key(), status });
    Ok(())
}

#[derive(Accounts)]
pub struct CloseCampaign<'info> {
    #[account(mut)]
    pub sponsor: Signer<'info>,
    #[account(
        mut,
        has_one = sponsor @ OracleError::NotSponsor,
        constraint = campaign.status != CampaignStatus::Closed @ OracleError::CampaignClosed
    )]
    pub campaign: Account<'info, Campaign>,
    #[account(address = campaign.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    #[account(mut, address = campaign.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(mut, token::mint = mint, token::authority = sponsor, token::token_program = token_program)]
    pub sponsor_token: InterfaceAccount<'info, TokenAccount>,
    pub token_program: Interface<'info, TokenInterface>,
}

/// Refunds the remaining budget to the sponsor and closes the vault. Payout records stay.
pub fn close_campaign(ctx: Context<CloseCampaign>) -> Result<()> {
    let campaign = &ctx.accounts.campaign;
    let seeds: &[&[u8]] = &[CAMPAIGN_SEED, campaign.campaign_id.as_ref(), &[campaign.bump]];
    let signer = &[seeds];
    let remaining = ctx.accounts.vault.amount;

    if remaining > 0 {
        transfer_checked(
            CpiContext::new_with_signer(
                ctx.accounts.token_program.to_account_info(),
                TransferChecked {
                    from: ctx.accounts.vault.to_account_info(),
                    mint: ctx.accounts.mint.to_account_info(),
                    to: ctx.accounts.sponsor_token.to_account_info(),
                    authority: campaign.to_account_info(),
                },
                signer,
            ),
            remaining,
            ctx.accounts.mint.decimals,
        )?;
    }
    close_account(CpiContext::new_with_signer(
        ctx.accounts.token_program.to_account_info(),
        CloseAccount {
            account: ctx.accounts.vault.to_account_info(),
            destination: ctx.accounts.sponsor.to_account_info(),
            authority: campaign.to_account_info(),
        },
        signer,
    ))?;

    ctx.accounts.campaign.status = CampaignStatus::Closed;
    emit!(CampaignClosed { campaign: ctx.accounts.campaign.key(), refunded: remaining });
    Ok(())
}
