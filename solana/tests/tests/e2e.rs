//! End-to-end: CRE-style reports go through Chainlink's real mock forwarder program
//! (the one `cre workflow simulate` uses) into contrib_oracle, both compiled to SBF.
//! Build first: cargo-build-sbf --arch v0 --manifest-path programs/contrib-oracle/Cargo.toml --sbf-out-dir target/deploy

use std::str::FromStr;

use anchor_lang::{
    prelude::Pubkey,
    solana_program::{
        instruction::{AccountMeta, Instruction},
        program_pack::Pack,
    },
    AccountDeserialize, AnchorSerialize, InstructionData, ToAccountMetas,
};
use contrib_oracle::{accounts as acc, instruction as ix, report::RewardReport, state::*, ID as PROGRAM_ID};
use litesvm::{types::FailedTransactionMetadata, LiteSVM};
use sha2::{Digest, Sha256};
use solana_keypair::Keypair;
use solana_signer::Signer;
use solana_transaction::Transaction;

const MOCK_FORWARDER: &str = "7kuEAA3mSC1Tz8gQjnvH7bKFda9xSPRRin9SZbH49cNK";
const USDC: u64 = 1_000_000; // 6 decimals
const POLICY: [u8; 32] = [7u8; 32];
const NO_OWNER: [u8; 20] = [0u8; 20];

fn sha256(b: &[u8]) -> [u8; 32] {
    Sha256::digest(b).into()
}

fn disc(name: &str) -> [u8; 8] {
    sha256(format!("global:{name}").as_bytes())[..8].try_into().unwrap()
}

fn pda(seeds: &[&[u8]], program: &Pubkey) -> Pubkey {
    Pubkey::find_program_address(seeds, program).0
}

struct Env {
    svm: LiteSVM,
    admin: Keypair,
    sponsor: Keypair,
    contributor: Keypair,
    forwarder: Pubkey,
    fwd_state: Pubkey,
    config: Pubkey,
    mint: Pubkey,
    campaign_id: [u8; 16],
    campaign: Pubkey,
    vault: Pubkey,
    sponsor_ata: Pubkey,
    contributor_ata: Pubkey,
    rent_payer: Pubkey,
}

impl Env {
    fn send(&mut self, ixs: &[Instruction], signers: &[&Keypair]) -> Result<(), FailedTransactionMetadata> {
        let tx = Transaction::new_signed_with_payer(ixs, Some(&signers[0].pubkey()), signers, self.svm.latest_blockhash());
        let res = self.svm.send_transaction(tx).map(|_| ());
        self.svm.expire_blockhash(); // Identical retries get a fresh signature.
        res
    }

    fn admin_send(&mut self, ixs: &[Instruction], extra: &[&Keypair]) -> Result<(), FailedTransactionMetadata> {
        let admin = self.admin.insecure_clone();
        let mut signers = vec![&admin];
        signers.extend_from_slice(extra);
        self.send(ixs, &signers)
    }

    fn balance(&self, token_account: &Pubkey) -> u64 {
        let a = self.svm.get_account(token_account).expect("token account");
        spl_token::state::Account::unpack(&a.data).unwrap().amount
    }

    fn campaign_state(&self) -> Campaign {
        let a = self.svm.get_account(&self.campaign).unwrap();
        Campaign::try_deserialize(&mut a.data.as_slice()).unwrap()
    }

    fn payout_pda(&self, contribution_id: &[u8; 32]) -> Pubkey {
        pda(&[PAYOUT_SEED, self.campaign.as_ref(), contribution_id], &PROGRAM_ID)
    }

    fn ata(&mut self, owner: &Pubkey) -> Pubkey {
        let ata = spl_associated_token_account::get_associated_token_address(owner, &self.mint);
        let create = spl_associated_token_account::instruction::create_associated_token_account(
            &self.admin.pubkey(), owner, &self.mint, &spl_token::ID,
        );
        self.admin_send(&[create], &[]).unwrap();
        ata
    }

    fn report(&self, contribution: &str, amount: u64) -> RewardReport {
        RewardReport {
            campaign_id: self.campaign_id,
            contribution_id: sha256(contribution.as_bytes()),
            recipient: self.contributor.pubkey(),
            amount,
            score: 91,
            evaluation_hash: [9u8; 32],
            policy_hash: POLICY,
        }
    }

    /// The accounts contrib_oracle's on_report expects after the forwarder's two.
    fn receiver_accounts(&self, r: &RewardReport, recipient_token: Pubkey) -> Vec<AccountMeta> {
        vec![
            AccountMeta::new_readonly(self.config, false),
            AccountMeta::new(self.campaign, false),
            AccountMeta::new(self.vault, false),
            AccountMeta::new_readonly(self.mint, false),
            AccountMeta::new(self.payout_pda(&r.contribution_id), false),
            AccountMeta::new(recipient_token, false),
            AccountMeta::new(self.rent_payer, false),
            AccountMeta::new_readonly(spl_token::ID, false),
            AccountMeta::new_readonly(anchor_lang::system_program::ID, false),
        ]
    }

    /// Mock forwarder `report` instruction, laid out exactly like a CRE write report.
    fn forwarder_ix(&self, state: Pubkey, r: &RewardReport, recipient_token: Pubkey, workflow_owner: [u8; 20], transmitter: &Pubkey) -> Instruction {
        let authority = pda(&[b"forwarder", state.as_ref(), PROGRAM_ID.as_ref()], &self.forwarder);
        let receiver = self.receiver_accounts(r, recipient_token);

        let mut keys = state.to_bytes().to_vec();
        keys.extend_from_slice(&authority.to_bytes());
        for m in &receiver {
            keys.extend_from_slice(&m.pubkey.to_bytes());
        }
        let account_hash = sha256(&keys);

        let mut raw = vec![1u8]; // forwarder metadata (45): version, execution id, timestamp, don id, config version
        raw.extend_from_slice(&sha256(&r.contribution_id)); // execution id
        raw.extend_from_slice(&[0u8; 12]);
        raw.extend_from_slice(&[3u8; 32]); // workflow metadata (64): cid, name, owner, report id
        raw.extend_from_slice(&[4u8; 10]);
        raw.extend_from_slice(&workflow_owner);
        raw.extend_from_slice(&[0, 1]);
        raw.extend_from_slice(&account_hash); // ForwarderReport { account_hash, payload: Vec<u8> }
        r.try_to_vec().unwrap().serialize(&mut raw).unwrap();

        let mut data = vec![0u8]; // no signatures (mock skips verification)
        data.extend_from_slice(&raw);
        data.extend_from_slice(&[0u8; 96]); // report context

        let mut ix_data = disc("report").to_vec();
        data.serialize(&mut ix_data).unwrap();

        let mut accounts = vec![
            AccountMeta::new_readonly(state, false),
            AccountMeta::new(*transmitter, true),
            AccountMeta::new_readonly(authority, false),
            AccountMeta::new_readonly(PROGRAM_ID, false),
            AccountMeta::new_readonly(anchor_lang::system_program::ID, false),
        ];
        accounts.extend(receiver);
        Instruction { program_id: self.forwarder, accounts, data: ix_data }
    }

    fn submit(&mut self, r: &RewardReport, recipient_token: Pubkey, workflow_owner: [u8; 20]) -> Result<(), FailedTransactionMetadata> {
        let ix = self.forwarder_ix(self.fwd_state, r, recipient_token, workflow_owner, &self.admin.pubkey());
        self.admin_send(&[ix], &[])
    }

    fn init_forwarder_state(&mut self) -> Pubkey {
        let state = Keypair::new();
        let ix = Instruction {
            program_id: self.forwarder,
            accounts: vec![
                AccountMeta::new(state.pubkey(), true),
                AccountMeta::new(self.admin.pubkey(), true),
                AccountMeta::new_readonly(anchor_lang::system_program::ID, false),
            ],
            data: disc("initialize").to_vec(),
        };
        self.admin_send(&[ix], &[&state]).unwrap();
        state.pubkey()
    }
}

fn setup(workflow_owner: [u8; 20]) -> Env {
    let mut svm = LiteSVM::new();
    svm.add_program(PROGRAM_ID, include_bytes!("../../target/deploy/contrib_oracle.so")).unwrap();
    let forwarder = Pubkey::from_str(MOCK_FORWARDER).unwrap();
    svm.add_program(forwarder, include_bytes!("../fixtures/mock_forwarder.so")).unwrap();

    let (admin, sponsor, contributor) = (Keypair::new(), Keypair::new(), Keypair::new());
    for k in [&admin, &sponsor, &contributor] {
        svm.airdrop(&k.pubkey(), 100_000_000_000).unwrap();
    }
    let campaign_id = *b"6f1c2a9e3b7d4c1e";
    let campaign = pda(&[CAMPAIGN_SEED, &campaign_id], &PROGRAM_ID);
    let mut env = Env {
        svm,
        admin,
        sponsor,
        contributor,
        forwarder,
        fwd_state: Pubkey::default(),
        config: pda(&[CONFIG_SEED], &PROGRAM_ID),
        mint: Pubkey::default(),
        campaign_id,
        campaign,
        vault: pda(&[VAULT_SEED, campaign.as_ref()], &PROGRAM_ID),
        sponsor_ata: Pubkey::default(),
        contributor_ata: Pubkey::default(),
        rent_payer: pda(&[RENT_PAYER_SEED], &PROGRAM_ID),
    };
    env.fwd_state = env.init_forwarder_state();

    // contrib_oracle config: trust the mock forwarder state.
    let init = Instruction {
        program_id: PROGRAM_ID,
        accounts: acc::Initialize { admin: env.admin.pubkey(), config: env.config, system_program: anchor_lang::system_program::ID }
            .to_account_metas(None),
        data: ix::Initialize { forwarder_program: forwarder, forwarder_state: env.fwd_state, workflow_owner }.data(),
    };
    env.admin_send(&[init], &[]).unwrap();

    // USDC-like mint (6 decimals), sponsor holds 10,000.
    let mint = Keypair::new();
    let rent = env.svm.minimum_balance_for_rent_exemption(spl_token::state::Mint::LEN);
    let create_mint = solana_system_interface::instruction::create_account(
        &env.admin.pubkey(), &mint.pubkey(), rent, spl_token::state::Mint::LEN as u64, &spl_token::ID,
    );
    let init_mint = spl_token::instruction::initialize_mint2(&spl_token::ID, &mint.pubkey(), &env.sponsor.pubkey(), None, 6).unwrap();
    env.admin_send(&[create_mint, init_mint], &[&mint]).unwrap();
    env.mint = mint.pubkey();
    env.sponsor_ata = env.ata(&env.sponsor.pubkey());
    env.contributor_ata = env.ata(&env.contributor.pubkey());
    let mint_to = spl_token::instruction::mint_to(&spl_token::ID, &env.mint, &env.sponsor_ata, &env.sponsor.pubkey(), &[], 10_000 * USDC).unwrap();
    let sponsor = env.sponsor.insecure_clone();
    env.send(&[mint_to], &[&sponsor]).unwrap();

    // Campaign: max 200 USDC per PR, funded with 1,000.
    let create = Instruction {
        program_id: PROGRAM_ID,
        accounts: acc::CreateCampaign {
            sponsor: env.sponsor.pubkey(), campaign: env.campaign, mint: env.mint, vault: env.vault,
            token_program: spl_token::ID, system_program: anchor_lang::system_program::ID,
        }.to_account_metas(None),
        data: ix::CreateCampaign { campaign_id, max_reward_per_pr: 200 * USDC, policy_hash: POLICY }.data(),
    };
    let fund = fund_ix(&env, 1_000 * USDC);
    env.send(&[create, fund], &[&sponsor]).unwrap();

    // Rent payer for payout records.
    let top_up = solana_system_interface::instruction::transfer(&env.admin.pubkey(), &env.rent_payer, 1_000_000_000);
    env.admin_send(&[top_up], &[]).unwrap();
    env
}

fn fund_ix(env: &Env, amount: u64) -> Instruction {
    Instruction {
        program_id: PROGRAM_ID,
        accounts: acc::FundCampaign {
            funder: env.sponsor.pubkey(), campaign: env.campaign, mint: env.mint, vault: env.vault,
            funder_token: env.sponsor_ata, token_program: spl_token::ID,
        }.to_account_metas(None),
        data: ix::FundCampaign { amount }.data(),
    }
}

fn status_ix(env: &Env, status: CampaignStatus) -> Instruction {
    Instruction {
        program_id: PROGRAM_ID,
        accounts: acc::SetCampaignStatus { sponsor: env.sponsor.pubkey(), campaign: env.campaign }.to_account_metas(None),
        data: ix::SetCampaignStatus { status }.data(),
    }
}

#[track_caller]
fn assert_err(res: Result<(), FailedTransactionMetadata>, code: &str) {
    let err = res.expect_err("transaction should fail");
    let logs = err.meta.logs.join("\n");
    assert!(logs.contains(&format!("Error Code: {code}")), "expected {code}, logs:\n{logs}");
}

#[test]
fn pays_contributor_through_forwarder_once() {
    let mut env = setup(NO_OWNER);
    let r = env.report("acme/pool#7", 150 * USDC);
    let ata = env.contributor_ata;

    env.submit(&r, ata, NO_OWNER).unwrap();
    assert_eq!(env.balance(&ata), 150 * USDC);
    assert_eq!(env.balance(&env.vault), 850 * USDC);

    let c = env.campaign_state();
    assert_eq!((c.total_paid, c.payout_count), (150 * USDC, 1));

    let payout = env.svm.get_account(&env.payout_pda(&r.contribution_id)).unwrap();
    let p = Payout::try_deserialize(&mut payout.data.as_slice()).unwrap();
    assert_eq!((p.recipient, p.amount, p.score, p.evaluation_hash), (env.contributor.pubkey(), 150 * USDC, 91, [9u8; 32]));
    assert_eq!(p.workflow_execution_report_id, [0, 1]);

    // A second report for the same PR (re-evaluation, retried webhook) never pays again.
    let mut again = r.clone();
    again.evaluation_hash = [8u8; 32];
    assert_err(env.submit(&again, ata, NO_OWNER), "AlreadyPaid");
    assert_eq!(env.balance(&ata), 150 * USDC);

    // A different PR pays.
    env.submit(&env.report("acme/pool#8", 10 * USDC), ata, NO_OWNER).unwrap();
    assert_eq!(env.balance(&ata), 160 * USDC);
}

#[test]
fn rejects_callers_that_are_not_the_forwarder() {
    let mut env = setup(NO_OWNER);
    let r = env.report("acme/pool#7", 100 * USDC);

    // Direct call, attacker signs as "forwarder_authority".
    let attacker = Keypair::new();
    env.svm.airdrop(&attacker.pubkey(), 1_000_000_000).unwrap();
    let direct = Instruction {
        program_id: PROGRAM_ID,
        accounts: acc::OnReport {
            forwarder_state: env.fwd_state, forwarder_authority: attacker.pubkey(), config: env.config,
            campaign: env.campaign, vault: env.vault, mint: env.mint, payout: env.payout_pda(&r.contribution_id),
            recipient_token: env.contributor_ata, rent_payer: env.rent_payer, token_program: spl_token::ID,
            system_program: anchor_lang::system_program::ID,
        }.to_account_metas(None),
        data: ix::OnReport { metadata: vec![0u8; 64], payload: r.try_to_vec().unwrap() }.data(),
    };
    assert_err(env.send(&[direct], &[&attacker]), "UnauthorizedForwarder");

    // Same forwarder program, but a forwarder state we don't trust.
    let rogue_state = env.init_forwarder_state();
    let rogue = env.forwarder_ix(rogue_state, &r, env.contributor_ata, NO_OWNER, &env.admin.pubkey());
    assert_err(env.admin_send(&[rogue], &[]), "UnauthorizedForwarder");

    assert_eq!(env.balance(&env.contributor_ata), 0);
}

#[test]
fn enforces_workflow_owner() {
    let owner = [0xAB; 20];
    let mut env = setup(owner);
    let r = env.report("acme/pool#7", 50 * USDC);
    assert_err(env.submit(&r, env.contributor_ata, [0xCD; 20]), "UnauthorizedWorkflow");
    env.submit(&r, env.contributor_ata, owner).unwrap();
    assert_eq!(env.balance(&env.contributor_ata), 50 * USDC);
}

#[test]
fn enforces_campaign_rules() {
    let mut env = setup(NO_OWNER);
    let ata = env.contributor_ata;

    let mut bad_policy = env.report("acme/pool#1", 10 * USDC);
    bad_policy.policy_hash = [1u8; 32];
    assert_err(env.submit(&bad_policy, ata, NO_OWNER), "PolicyMismatch");

    assert_err(env.submit(&env.report("acme/pool#2", 201 * USDC), ata, NO_OWNER), "RewardAboveCap");
    assert_err(env.submit(&env.report("acme/pool#3", 0), ata, NO_OWNER), "ZeroReward");

    // Token account owned by someone else than the report's recipient.
    let thief = Keypair::new().pubkey();
    let thief_ata = env.ata(&thief);
    assert_err(env.submit(&env.report("acme/pool#4", 10 * USDC), thief_ata, NO_OWNER), "RecipientMismatch");

    // Wrong campaign account for the report's campaign_id.
    let mut other = env.report("acme/pool#5", 10 * USDC);
    other.campaign_id = *b"0000000000000000";
    assert_err(env.submit(&other, ata, NO_OWNER), "CampaignMismatch");

    // Paused campaigns don't pay; resumed ones do.
    let sponsor = env.sponsor.insecure_clone();
    env.send(&[status_ix(&env, CampaignStatus::Paused)], &[&sponsor]).unwrap();
    assert_err(env.submit(&env.report("acme/pool#6", 10 * USDC), ata, NO_OWNER), "CampaignNotActive");
    env.send(&[status_ix(&env, CampaignStatus::Active)], &[&sponsor]).unwrap();
    env.submit(&env.report("acme/pool#6", 10 * USDC), ata, NO_OWNER).unwrap();

    assert_eq!(env.balance(&ata), 10 * USDC);
}

#[test]
fn cannot_pay_more_than_the_vault_holds() {
    let mut env = setup(NO_OWNER);
    let ata = env.contributor_ata;
    for pr in 0..5 {
        env.submit(&env.report(&format!("acme/pool#{pr}"), 200 * USDC), ata, NO_OWNER).unwrap();
    }
    assert_eq!(env.balance(&env.vault), 0);
    assert_err(env.submit(&env.report("acme/pool#99", 1 * USDC), ata, NO_OWNER), "InsufficientFunds");
}

#[test]
fn sponsor_closes_and_gets_the_rest_back() {
    let mut env = setup(NO_OWNER);
    env.submit(&env.report("acme/pool#7", 100 * USDC), env.contributor_ata, NO_OWNER).unwrap();

    let close = |env: &Env, sponsor: Pubkey, sponsor_token: Pubkey| Instruction {
        program_id: PROGRAM_ID,
        accounts: acc::CloseCampaign {
            sponsor, campaign: env.campaign, mint: env.mint, vault: env.vault, sponsor_token, token_program: spl_token::ID,
        }.to_account_metas(None),
        data: ix::CloseCampaign {}.data(),
    };

    // Not the sponsor.
    let intruder = env.contributor.insecure_clone();
    let ix = close(&env, intruder.pubkey(), env.contributor_ata);
    assert_err(env.send(&[ix], &[&intruder]), "NotSponsor");

    let sponsor = env.sponsor.insecure_clone();
    let ix = close(&env, sponsor.pubkey(), env.sponsor_ata);
    env.send(&[ix], &[&sponsor]).unwrap();
    assert_eq!(env.balance(&env.sponsor_ata), 9_900 * USDC); // 10,000 - 100 paid out
    assert!(env.svm.get_account(&env.vault).map_or(true, |a| a.lamports == 0), "vault closed");
    assert_eq!(env.campaign_state().status, CampaignStatus::Closed);

    // Vault account is gone, so a report fails before the status check.
    assert_err(env.submit(&env.report("acme/pool#8", 1 * USDC), env.contributor_ata, NO_OWNER), "AccountNotInitialized");
}
