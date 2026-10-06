import { sha256, stringToBytes } from "viem";

// Stable JSON: sorted keys, no whitespace. Same input -> same hash on every node.
export const canonicalJson = (value: unknown): string => {
  if (value === null || typeof value !== "object") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalJson).join(",")}]`;
  const obj = value as Record<string, unknown>;
  const keys = Object.keys(obj)
    .filter((k) => obj[k] !== undefined)
    .sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${canonicalJson(obj[k])}`).join(",")}}`;
};

export const hashJson = (value: unknown): string => sha256(stringToBytes(canonicalJson(value)));
