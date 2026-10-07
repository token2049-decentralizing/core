use anchor_lang::prelude::*;

use crate::errors::OracleError;

/// Payload the CRE workflow writes (Borsh). `on_report` receives it as `payload`.
#[derive(AnchorSerialize, AnchorDeserialize, Clone, Debug, PartialEq)]
pub struct RewardReport {
    /// cre-runner campaign UUID, raw 16 bytes.
    pub campaign_id: [u8; 16],
    /// sha256("owner/repo#pr_number"): one payout per PR per campaign.
    pub contribution_id: [u8; 32],
    /// Contributor wallet; the destination token account must belong to it.
    pub recipient: Pubkey,
    /// Token base units.
    pub amount: u64,
    pub score: u8,
    pub evaluation_hash: [u8; 32],
    pub policy_hash: [u8; 32],
}

impl RewardReport {
    /// Strict decode: no trailing bytes.
    pub fn decode(payload: &[u8]) -> Result<Self> {
        let mut bytes = payload;
        let report = RewardReport::deserialize(&mut bytes).map_err(|_| OracleError::InvalidPayload)?;
        require!(bytes.is_empty(), OracleError::InvalidPayload);
        Ok(report)
    }
}

/// Workflow metadata the forwarder passes: cid (32) | name (10) | owner (20) | report_id (2).
pub struct ReportMetadata<'a> {
    pub workflow_owner: &'a [u8],
    pub report_id: [u8; 2],
}

pub const METADATA_LEN: usize = 64;

impl<'a> ReportMetadata<'a> {
    pub fn parse(metadata: &'a [u8]) -> Result<Self> {
        require!(metadata.len() == METADATA_LEN, OracleError::InvalidMetadata);
        Ok(Self {
            workflow_owner: &metadata[42..62],
            report_id: [metadata[62], metadata[63]],
        })
    }
}
