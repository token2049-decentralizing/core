// Solana cluster the rewards are paid on; drives explorer links.
export const SOLANA_CLUSTER = process.env.NEXT_PUBLIC_SOLANA_CLUSTER ?? "devnet"

export function explorerAddressUrl(address: string) {
  const cluster =
    SOLANA_CLUSTER === "mainnet-beta" ? "" : `?cluster=${SOLANA_CLUSTER}`
  return `https://explorer.solana.com/address/${address}${cluster}`
}

export function explorerTxUrl(signature: string) {
  const cluster =
    SOLANA_CLUSTER === "mainnet-beta" ? "" : `?cluster=${SOLANA_CLUSTER}`
  return `https://explorer.solana.com/tx/${signature}${cluster}`
}

export function shortAddress(address: string) {
  return address.length > 12
    ? `${address.slice(0, 4)}…${address.slice(-4)}`
    : address
}
