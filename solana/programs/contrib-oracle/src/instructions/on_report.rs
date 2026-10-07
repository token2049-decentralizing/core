use anchor_lang::prelude::*;
use anchor_lang::solana_program::{program::invoke_signed, system_instruction};
use anchor_spl::token_interface::{transfer_checked, Mint, TokenAccount, TokenInterface, TransferChecked};

use crate::errors::OracleError;
use crate::events::RewardPaid;
use crate::report::{ReportMetadata, RewardReport};
use crate::state::*;

/// Account order is fixed by the keystone forwarder: [forwarder_state, forwarder_authority, ...ours].
/// The forwarder passes our accounts without signer flags, so none of them can sign here.
#[derive(Accounts)]
pub struct OnReport<'info> {
    /// CHECK: must equal config.forwarder_state (checked in handler).
    pub forwarder_state: UncheckedAccount<'info>,
    /// PDA ["forwarder", forwarder_state, this program] of the forwarder program.
    /// Only the forwarder can sign for it, so this is the caller authentication.
    pub forwarder_authority: Signer<'info>,
    #[account(seeds = [CONFIG_SEED], bump = config.bump)]
    pub config: Account<'info, Config>,
    #[account(mut)]
    pub campaign: Account<'info, Campaign>,
    #[account(mut, address = campaign.vault)]
    pub vault: InterfaceAccount<'info, TokenAccount>,
    #[account(address = campaign.mint)]
    pub mint: InterfaceAccount<'info, Mint>,
    /// CHECK: PDA ["payout", campaign, contribution_id]; created here, must not exist yet.
    #[account(mut)]
    pub payout: UncheckedAccount<'info>,
    #[account(mut, token::mint = mint, token::token_program = token_program)]
    pub recipient_token: InterfaceAccount<'info, TokenAccount>,
    /// System-owned PDA holding SOL that pays rent for payout records.
    #[account(mut, seeds = [RENT_PAYER_SEED], bump)]
    pub rent_payer: SystemAccount<'info>,
    pub token_program: Interface<'info, TokenInterface>,
    pub system_program: Program<'info, System>,
}

pub fn on_report(ctx: Context<OnReport>, metadata: Vec<u8>, payload: Vec<u8>) -> Result<()> {
    let config = &ctx.accounts.config;

    // 1. Caller is the configured forwarder.
    require_keys_eq!(ctx.accounts.forwarder_state.key(), config.forwarder_state, OracleError::UnauthorizedForwarder);
    let (expected_authority, _) = Pubkey::find_program_address(
        &[FORWARDER_AUTHORITY_SEED, config.forwarder_state.as_ref(), crate::ID.as_ref()],
        &config.forwarder_program,
    );
    require_keys_eq!(ctx.accounts.forwarder_authority.key(), expected_authority, OracleError::UnauthorizedForwarder);

    // 2. Report comes from the allowed workflow (if one is configured).
    let meta = ReportMetadata::parse(&metadata)?;
    if config.workflow_owner != [0u8; 20] {
        require!(meta.workflow_owner == config.workflow_owner, OracleError::UnauthorizedWorkflow);
    }

    // 3. Campaign rules.
    let report = RewardReport::decode(&payload)?;
    let campaign = &ctx.accounts.campaign;
    let (expected_campaign, _) =
        Pubkey::find_program_address(&[CAMPAIGN_SEED, report.campaign_id.as_ref()], &crate::ID);
    require_keys_eq!(campaign.key(), expected_campaign, OracleError::CampaignMismatch);
    require!(campaign.status == CampaignStatus::Active, OracleError::CampaignNotActive);
    if campaign.policy_hash != [0u8; 32] {
        require!(report.policy_hash == campaign.policy_hash, OracleError::PolicyMismatch);
    }
    require!(report.amount > 0, OracleError::ZeroReward);
    require!(report.amount <= campaign.max_reward_per_pr, OracleError::RewardAboveCap);
    require!(ctx.accounts.vault.amount >= report.amount, OracleError::InsufficientFunds);
    require_keys_eq!(ctx.accounts.recipient_token.owner, report.recipient, OracleError::RecipientMismatch);

    // 4. Pay-once record. Creating it fails if this PR was already paid.
    let campaign_key = campaign.key();
    let (expected_payout, payout_bump) = Pubkey::find_program_address(
        &[PAYOUT_SEED, campaign_key.as_ref(), report.contribution_id.as_ref()],
        &crate::ID,
    );
    let payout_info = ctx.accounts.payout.to_account_info();
    require_keys_eq!(payout_info.key(), expected_payout, OracleError::PayoutMismatch);
    require!(payout_info.lamports() == 0 && payout_info.data_is_empty(), OracleError::AlreadyPaid);

    let space = 8 + Payout::INIT_SPACE;
    let lamports = Rent::get()?.minimum_balance(space);
    invoke_signed(
        &system_instruction::create_account(
            ctx.accounts.rent_payer.key,
            payout_info.key,
            lamports,
            space as u64,
            &crate::ID,
        ),
        &[
            ctx.accounts.rent_payer.to_account_info(),
            payout_info.clone(),
            ctx.accounts.system_program.to_account_info(),
        ],
        &[
            &[RENT_PAYER_SEED, &[ctx.bumps.rent_payer]],
            &[PAYOUT_SEED, campaign_key.as_ref(), report.contribution_id.as_ref(), &[payout_bump]],
        ],
    )?;
    let record = Payout {
        campaign: campaign_key,
        contribution_id: report.contribution_id,
        recipient: report.recipient,
        amount: report.amount,
        score: report.score,
        evaluation_hash: report.evaluation_hash,
        policy_hash: report.policy_hash,
        workflow_execution_report_id: meta.report_id,
        paid_at: Clock::get()?.unix_timestamp,
    };
    record.try_serialize(&mut &mut payout_info.try_borrow_mut_data()?[..])?;

    // 5. Pay from the vault, signing as the campaign.
    let seeds: &[&[u8]] = &[CAMPAIGN_SEED, campaign.campaign_id.as_ref(), &[campaign.bump]];
    transfer_checked(
        CpiContext::new_with_signer(
            ctx.accounts.token_program.to_account_info(),
            TransferChecked {
                from: ctx.accounts.vault.to_account_info(),
                mint: ctx.accounts.mint.to_account_info(),
                to: ctx.accounts.recipient_token.to_account_info(),
                authority: campaign.to_account_info(),
            },
            &[seeds],
        ),
        report.amount,
        ctx.accounts.mint.decimals,
    )?;

    let campaign = &mut ctx.accounts.campaign;
    campaign.total_paid = campaign.total_paid.checked_add(report.amount).ok_or(OracleError::Overflow)?;
    campaign.payout_count = campaign.payout_count.checked_add(1).ok_or(OracleError::Overflow)?;

    emit!(RewardPaid { campaign: campaign_key, payout: payout_info.key(), report });
    Ok(())
}
