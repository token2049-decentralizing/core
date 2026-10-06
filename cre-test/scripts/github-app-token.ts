// Mints a short-lived (1h), read-only GitHub App installation token and prints it.
// Usage: bun scripts/github-app-token.ts
// Env: GITHUB_APP_ID, GITHUB_APP_PRIVATE_KEY_PATH, and GITHUB_APP_INSTALLATION_ID or GITHUB_APP_REPO ("owner/repo").
import { createSign } from "node:crypto";
import { readFileSync } from "node:fs";

const API = process.env.GITHUB_API_URL ?? "https://api.github.com";

// Only what the workflow reads. Token is downscoped even if the App has more.
const READ_ONLY = { pull_requests: "read", contents: "read", checks: "read", metadata: "read" };

const b64url = (s: string | Buffer) => Buffer.from(s).toString("base64url");

// App JWT (RS256), valid 9 min. iat is backdated 60s for clock drift.
export const appJwt = (appId: string, privateKeyPem: string, now = Math.floor(Date.now() / 1000)): string => {
  const header = b64url(JSON.stringify({ alg: "RS256", typ: "JWT" }));
  const payload = b64url(JSON.stringify({ iat: now - 60, exp: now + 540, iss: appId }));
  const sig = createSign("RSA-SHA256").update(`${header}.${payload}`).sign(privateKeyPem);
  return `${header}.${payload}.${b64url(sig)}`;
};

const gh = async (path: string, jwt: string, body?: unknown) => {
  const res = await fetch(`${API}${path}`, {
    method: body ? "POST" : "GET",
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${jwt}`,
      "X-GitHub-Api-Version": "2022-11-28",
      "User-Agent": "contriboracle",
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) throw new Error(`GitHub ${path} -> HTTP ${res.status}: ${await res.text()}`);
  return res.json() as Promise<any>;
};

const need = (name: string) => {
  const v = process.env[name];
  if (!v) throw new Error(`missing env ${name}`);
  return v;
};

if (import.meta.main) {
  const jwt = appJwt(need("GITHUB_APP_ID"), readFileSync(need("GITHUB_APP_PRIVATE_KEY_PATH"), "utf8"));

  const installationId =
    process.env.GITHUB_APP_INSTALLATION_ID || (await gh(`/repos/${need("GITHUB_APP_REPO")}/installation`, jwt)).id;

  const { token } = await gh(`/app/installations/${installationId}/access_tokens`, jwt, { permissions: READ_ONLY });
  process.stdout.write(token);
}
