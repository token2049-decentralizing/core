// Rewards and campaign treasuries live on Solana devnet only.
export const SOLANA_CLUSTER = "devnet"

export function explorerAddressUrl(address: string) {
  return `https://explorer.solana.com/address/${address}?cluster=${SOLANA_CLUSTER}`
}

export function explorerTxUrl(signature: string) {
  return `https://explorer.solana.com/tx/${signature}?cluster=${SOLANA_CLUSTER}`
}

export function shortAddress(address: string) {
  return address.length > 12
    ? `${address.slice(0, 4)}…${address.slice(-4)}`
    : address
}
