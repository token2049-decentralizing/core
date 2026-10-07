// contrib_oracle admin CLI. Wallet: ANCHOR_WALLET or ~/.config/solana/id.json. RPC: SOLANA_RPC_URL (default devnet).
import { AnchorProvider, Wallet } from "@coral-xyz/anchor";
import { createMint, getMint, getOrCreateAssociatedTokenAccount, mintTo } from "@solana/spl-token";
import { Connection, Keypair, LAMPORTS_PER_SOL, PublicKey, SystemProgram, Transaction } from "@solana/web3.js";
import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { parseArgs } from "node:util";
import { DEVNET_MOCK_FORWARDER, DEVNET_MOCK_FORWARDER_STATE, Treasury, contributionId, type Status } from "./treasury";

const USAGE = `usage: bun cli.ts <command> [args]

  init [--workflow-owner 0x<20 bytes>] [--forwarder <program>] [--forwarder-state <state>]
  update-config [same flags as init]
  create-mint [--decimals 6] [--amount 10000]           demo token, minted to your wallet
  create-campaign <uuid> <mint> <max-reward-per-pr> [--policy-hash 0x<32 bytes>]
  fund <uuid> <amount>                                    from your associated token account
  fund-rent-payer <sol>                                   SOL that pays rent for payout records
  status <uuid> active|paused
  close <uuid>                                            refund the rest to you, close the vault
  show <uuid>
  ensure-ata <mint> <owner>                               recipient token account (payouts need it)
  payout <uuid> <owner/repo> <pr>
  accounts <uuid> <owner/repo> <pr> <recipient>           remaining accounts for the CRE write

Amounts are whole tokens ("150", "0.5"). Forwarder defaults to the devnet mock forwarder.`;

function loadWallet(): Keypair {
    const path = process.env.ANCHOR_WALLET ?? `${homedir()}/.config/solana/id.json`;
    return Keypair.fromSecretKey(Uint8Array.from(JSON.parse(readFileSync(path, "utf8"))));
}

// "150.5" -> base units, refusing more precision than the mint has.
function toBaseUnits(amount: string, decimals: number): bigint {
    const m = /^(\d+)(?:\.(\d+))?$/.exec(amount);
    if (!m) throw new Error(`invalid amount: ${amount}`);
    const frac = m[2] ?? "";
    if (frac.length > decimals) throw new Error(`${amount} has more than ${decimals} decimals`);
    return BigInt(m[1]) * 10n ** BigInt(decimals) + BigInt(frac.padEnd(decimals, "0") || "0");
}

const hex = (b: Uint8Array | number[]) => "0x" + Buffer.from(b).toString("hex");

async function main() {
    const { values: flags, positionals } = parseArgs({
        allowPositionals: true,
        options: {
            "workflow-owner": { type: "string" },
            forwarder: { type: "string" },
            "forwarder-state": { type: "string" },
            "policy-hash": { type: "string" },
            decimals: { type: "string", default: "6" },
            amount: { type: "string", default: "10000" },
            "program-id": { type: "string" },
        },
    });
    const [cmd, ...args] = positionals;
    const need = (n: number) => {
        if (args.length < n) throw new Error(USAGE);
    };

    if (!cmd || cmd === "help") {
        console.log(USAGE);
        return;
    }
    const keypair = loadWallet();
    const connection = new Connection("https://devnet.helius-rpc.com/?api-key=e2d0552d-b87c-4b50-b876-5d295f5e85e5");
    const provider = new AnchorProvider(connection, new Wallet(keypair), { commitment: "confirmed" });
    const t = new Treasury(provider, flags["program-id"] ? new PublicKey(flags["program-id"]) : undefined);
    const forwarder = new PublicKey(flags.forwarder ?? DEVNET_MOCK_FORWARDER);
    const forwarderState = new PublicKey(flags["forwarder-state"] ?? DEVNET_MOCK_FORWARDER_STATE);
    const out = (v: unknown) => console.log(JSON.stringify(v, (_, x) => (typeof x === "bigint" ? x.toString() : x), 2));

    switch (cmd) {
        case "init":
            out({ tx: await t.initialize(forwarder, forwarderState, flags["workflow-owner"]), config: t.config.toBase58() });
            break;
        case "update-config":
            out({ tx: await t.updateConfig(forwarder, forwarderState, flags["workflow-owner"]) });
            break;
        case "create-mint": {
            const decimals = Number(flags.decimals);
            const mint = await createMint(connection, keypair, keypair.publicKey, null, decimals);
            const ata = await getOrCreateAssociatedTokenAccount(connection, keypair, mint, keypair.publicKey);
            await mintTo(connection, keypair, mint, ata.address, keypair, toBaseUnits(flags.amount!, decimals));
            out({ mint: mint.toBase58(), decimals, minted: flags.amount, to: ata.address.toBase58() });
            break;
        }
        case "create-campaign": {
            need(3);
            const mint = new PublicKey(args[1]);
            const { decimals } = await getMint(connection, mint, undefined, await t.tokenProgram(mint));
            const tx = await t.createCampaign(args[0], mint, toBaseUnits(args[2], decimals), flags["policy-hash"]);
            out({ tx, campaign: t.campaign(args[0]).toBase58(), vault: t.vault(args[0]).toBase58() });
            break;
        }
        case "fund": {
            need(2);
            const c = await t.fetchCampaign(args[0]);
            const { decimals } = await getMint(connection, c.mint, undefined, await t.tokenProgram(c.mint));
            out({ tx: await t.fund(args[0], toBaseUnits(args[1], decimals)) });
            break;
        }
        case "fund-rent-payer": {
            need(1);
            const lamports = Number(toBaseUnits(args[0], 9));
            const tx = new Transaction().add(SystemProgram.transfer({ fromPubkey: keypair.publicKey, toPubkey: t.rentPayer, lamports }));
            out({ tx: await provider.sendAndConfirm(tx), rentPayer: t.rentPayer.toBase58(), lamports });
            break;
        }
        case "status":
            need(2);
            if (args[1] !== "active" && args[1] !== "paused") throw new Error("status must be active or paused");
            out({ tx: await t.setStatus(args[0], args[1] as Status) });
            break;
        case "close":
            need(1);
            out({ tx: await t.close(args[0]) });
            break;
        case "show": {
            need(1);
            const c = await t.fetchCampaign(args[0]);
            const vault = await connection.getTokenAccountBalance(c.vault).catch(() => null);
            const rent = await connection.getBalance(t.rentPayer);
            out({
                programId: t.programId.toBase58(),
                campaign: t.campaign(args[0]).toBase58(),
                sponsor: c.sponsor.toBase58(),
                mint: c.mint.toBase58(),
                vault: c.vault.toBase58(),
                vaultBalance: vault?.value.uiAmountString ?? "closed",
                maxRewardPerPr: c.maxRewardPerPr.toString(),
                policyHash: hex(c.policyHash),
                status: Object.keys(c.status)[0],
                totalPaid: c.totalPaid.toString(),
                payoutCount: c.payoutCount.toString(),
                rentPayerSol: rent / LAMPORTS_PER_SOL,
            });
            break;
        }
        case "ensure-ata": {
            need(2);
            const mint = new PublicKey(args[0]);
            const ata = await getOrCreateAssociatedTokenAccount(
                connection,
                keypair,
                mint,
                new PublicKey(args[1]),
                true,
                undefined,
                undefined,
                await t.tokenProgram(mint),
            );
            out({ tokenAccount: ata.address.toBase58() });
            break;
        }
        case "payout": {
            need(3);
            const p = await t.fetchPayout(args[0], args[1], Number(args[2]));
            out(
                p && {
                    ...p,
                    campaign: p.campaign.toBase58(),
                    recipient: p.recipient.toBase58(),
                    contributionId: hex(p.contributionId),
                    evaluationHash: hex(p.evaluationHash),
                    reportId: hex(p.reportId),
                },
            );
            break;
        }
        case "accounts": {
            need(4);
            const metas = await t.reportAccounts(args[0], args[1], Number(args[2]), new PublicKey(args[3]));
            out({
                contributionId: hex(contributionId(args[1], Number(args[2]))),
                remainingAccounts: metas.map((m) => ({ pubkey: m.pubkey.toBase58(), writable: m.isWritable })),
            });
            break;
        }
        default:
            throw new Error(USAGE);
    }
}

main().catch((err) => {
    console.error(err instanceof Error ? err.message : err);
    if (err?.logs) console.error(err.logs.join("\n"));
    process.exit(1);
});
