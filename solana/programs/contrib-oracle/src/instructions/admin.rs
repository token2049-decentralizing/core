use anchor_lang::prelude::*;

use crate::errors::OracleError;
use crate::state::*;

#[derive(Accounts)]
pub struct Initialize<'info> {
    #[account(mut)]
    pub admin: Signer<'info>,
    #[account(init, payer = admin, space = 8 + Config::INIT_SPACE, seeds = [CONFIG_SEED], bump)]
    pub config: Account<'info, Config>,
    pub system_program: Program<'info, System>,
}

/// One-time setup. Run it right after deploying: the first caller becomes admin.
pub fn initialize(
    ctx: Context<Initialize>,
    forwarder_program: Pubkey,
    forwarder_state: Pubkey,
    workflow_owner: [u8; 20],
) -> Result<()> {
    ctx.accounts.config.set_inner(Config {
        admin: ctx.accounts.admin.key(),
        forwarder_program,
        forwarder_state,
        workflow_owner,
        bump: ctx.bumps.config,
    });
    Ok(())
}

#[derive(Accounts)]
pub struct UpdateConfig<'info> {
    pub admin: Signer<'info>,
    #[account(mut, seeds = [CONFIG_SEED], bump = config.bump, has_one = admin @ OracleError::NotAdmin)]
    pub config: Account<'info, Config>,
}

/// Switch forwarder (mock for simulation, keystone for production) or workflow owner.
pub fn update_config(
    ctx: Context<UpdateConfig>,
    forwarder_program: Pubkey,
    forwarder_state: Pubkey,
    workflow_owner: [u8; 20],
) -> Result<()> {
    let config = &mut ctx.accounts.config;
    config.forwarder_program = forwarder_program;
    config.forwarder_state = forwarder_state;
    config.workflow_owner = workflow_owner;
    Ok(())
}
