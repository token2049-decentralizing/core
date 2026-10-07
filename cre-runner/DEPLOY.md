# cre-runner 部署到 Fly

一个 Fly app（`cre-runner`，区域 `sin`）跑全部功能：webhook 接收、前端 API、CRE workflow 执行（镜像内置 `cre` CLI 和预编译 WASM）、LLM reviewer。
以下命令都在 `cre-runner/` 目录执行（`fly.toml` 在这里）。

## 0. 前置条件

- `flyctl` 已安装并登录：`fly auth login`
- 本机有 `cre` CLI（`cre version`），用于生成 CRE 登录会话
- 手边有：Supabase 项目 URL 和 secret key、GitHub App（App ID、私钥 `github-app.pem`、webhook secret）、LLM API key 和 model
- 前端的线上域名（用于 CORS），例如 `https://cre-runner-frontend.vercel.app`

## 1. Supabase：建表

在 Supabase SQL Editor 里按顺序执行 `migrations/` 下还没执行过的文件。新库从 `001_github_webhook_events.sql` 开始，按顺序全部执行。

| 文件 | 内容 |
|------|------|
| `003_cre_executions.sql` | CRE 执行记录表 `cre_executions` |
| `004_contributor_wallets.sql` | PR 作者和收款钱包字段，`contributor_wallets` 表（GitHub 账号到 Privy Solana 钱包的映射） |
| `005_payout_tx.sql` | `payout_tx`：链上发奖的 Solana 交易 |

**先执行 004，再部署带 Privy 的版本**。新版本会往 `cre_executions` 写 `author_login`、`recipient_wallet` 等列，缺列时执行记录会写入失败。

## 2. Fly app

`fly.toml` 里的 app 名是 `cre-runner`。先看它是否已经存在：

```bash
fly status
```

- 已存在：跳到第 3 步。
- 不存在：`fly apps create cre-runner`。名字被占用时换一个，并改掉 `fly.toml` 的 `app`。
  下文的 `cre-runner.fly.dev` 也要相应替换。

建议只跑一台机器。多台机器也能保证只结算一次（靠数据库唯一索引），但 reviewer 缓存和排队是每台机器各自一份，单机更简单：

```bash
fly scale count 1
```

## 3. Secrets

下面全部用 `--stage` 暂存，第 5 步 deploy 时一次性生效，避免每设一个就重启一次。

### 3.1 基础

```bash
fly secrets set --stage \
  SUPABASE_URL="https://<project>.supabase.co" \
  SUPABASE_SECRET_KEY="<secret key>" \
  GITHUB_WEBHOOK_SECRET="<GitHub App 里配置的 webhook secret>" \
  CORS_ALLOWED_ORIGINS="https://<前端域名>"
```

`GITHUB_WEBHOOK_SECRET` 必须设置：没有它 webhook 照样入库，但**不会触发任何 CRE 执行**（防止有人伪造 webhook 消耗额度）。

### 3.2 GitHub App

```bash
fly secrets set --stage \
  GITHUB_APP_ID="<App ID>" \
  GITHUB_APP_PRIVATE_KEY="$(cat github-app.pem)"
```

- 私钥直接放 PEM 内容，不需要挂文件。
- 不用设 `GITHUB_APP_INSTALLATION_ID`：每个仓库会自动查找它所在的 installation。

### 3.3 LLM reviewer

```bash
fly secrets set --stage \
  LLM_API_KEY="<key>" \
  LLM_MODEL="<model ID 或 ep-...>"
# 可选：LLM_BASE_URL（默认 BytePlus ap-southeast）、LLM_EXTRA_BODY='{"thinking":{"type":"disabled"}}'、LLM_JSON_MODE=true
```

不设 `LLM_API_KEY` / `LLM_MODEL` 也能跑，只是 reviewer 分数会用 stub。

### 3.4 Privy（收款钱包）

merge 的 PR 会把奖励发到作者的 Privy Solana 钱包。作者从没登录过时，runner 会用他的 GitHub 账号预生成一个，作者以后用同一个 GitHub 账号登录就能拿到这个钱包。

```bash
fly secrets set --stage PRIVY_APP_ID="<App ID>" PRIVY_APP_SECRET="<App secret>"
```

两个值都在 Privy Dashboard → App settings → Basics。不设的话，merge 的 PR 照常评估，只是没有收款钱包，启动日志会提示。

Privy Dashboard 里还要配置：

1. **Login methods**：打开 **GitHub**。生产环境建议填你自己的 GitHub OAuth App 的 client ID / secret。
2. **Wallets → Embedded wallets**：打开 **Solana**。
3. **App settings → Domains**：加上前端域名（本地开发再加 `http://localhost:3000`）。

### 3.5 Solana 链上发奖（devnet）

合约在仓库根目录的 `solana/`（Anchor 程序 `contrib_oracle`）。

一次 merge 的发奖流程：

1. runner 读取链上 campaign 账户，得到奖励代币的 mint 和 token program。
2. runner 用 `CRE_SOLANA_PRIVATE_KEY` 给作者的 Privy 钱包创建代币账户（ATA）。
3. CRE workflow 生成 `RewardReport`，以 `cre workflow simulate --broadcast` 发出，经 mock forwarder 调用 `on_report`。
4. 合约检查规则后，从 campaign 的 vault 把代币转给作者，同时记录 `Payout` PDA，同一个 PR 第二次会被拒绝（`AlreadyPaid`）。
5. 交易签名写进 `cre_executions.payout_tx`，前端显示 "Paid on Solana ↗"。

以下命令需要 Solana CLI（`cargo-build-sbf`）、Anchor CLI 0.31.1 和 Bun，详见 `solana/README.md`。

**a. 部署合约**（如果 `solana program show <程序ID> --url devnet` 已经能查到，并且已经 `init` 过，跳到 c）

```bash
cd solana
solana config set --url devnet
solana-keygen new                     # 已有 ~/.config/solana/id.json 就跳过
solana airdrop 5                      # 不够就用 https://faucet.solana.com；部署约需 2.2 SOL
cargo-build-sbf --arch v0 --manifest-path programs/contrib-oracle/Cargo.toml --sbf-out-dir target/deploy
anchor keys sync                      # 把新程序 ID 写进 declare_id! 和 Anchor.toml
cargo-build-sbf --arch v0 --manifest-path programs/contrib-oracle/Cargo.toml --sbf-out-dir target/deploy
anchor idl build -p contrib_oracle -o idl/contrib_oracle.json
solana program deploy target/deploy/contrib_oracle.so --program-id target/deploy/contrib_oracle-keypair.json
```

程序 ID 变了以后，**重新生成 workflow 的 Go 绑定**（runner 运行时会用 `SOLANA_PROGRAM_ID` 覆盖，不重新生成也能用，但重新生成可以让 IDL 保持一致）：

```bash
cd ../cre-runner/cre && cre generate-bindings solana -i ../../solana/idl -l go
```

**b. 初始化**（部署后立刻做；`init` 谁先调用谁就是 admin）

```bash
cd solana/scripts && bun install
bun cli.ts init                       # 信任 devnet mock forwarder
bun cli.ts fund-rent-payer 0.5        # 每条 Payout 记录约 0.002 SOL 的租金
```

**c. 给每个 cre-runner campaign 建链上 campaign，并注资**

```bash
bun cli.ts create-mint --amount 10000                        # 演示用 6 位小数代币；或用 devnet USDC 的 mint
bun cli.ts create-campaign <campaign UUID> <mint> <max-reward-per-pr>
bun cli.ts fund <campaign UUID> 1000
bun cli.ts show <campaign UUID>
```

- `<campaign UUID>` 必须和前端/Supabase 里的 campaign ID 完全一样。
- `<max-reward-per-pr>` 用整币单位，填和 campaign 的 `max_reward_per_pr` 相同的值（链上会拒绝超过它的奖励）。
- 不传 `--policy-hash` 就不校验 policy（runner 每次生成的 workflow 配置都按 campaign 计算，policy hash 会随 campaign 设置变化）。

**d. 配置 runner**

runner 用同一把 key 创建 ATA，cre CLI 也用它签发奖交易，所以这个钱包要有一点 SOL（每个新收款人约 0.002 SOL，加交易费）：

```bash
solana-keygen new -o ~/cre-payer.json && solana airdrop 2 $(solana-keygen pubkey ~/cre-payer.json)
cd cre-runner
fly secrets set --stage \
  CRE_SOLANA_PRIVATE_KEY="$(go run ./cmd/solana-key ~/cre-payer.json)" \
  SOLANA_PROGRAM_ID="<程序 ID>"
fly secrets set --stage SOLANA_RPC_URL="https://devnet.helius-rpc.com/?api-key=<key>"
# SOLANA_RPC_URL：runner 和 CRE 模拟器（project.yaml 的 ${CRE_SOLANA_RPC_URL}）都用它。
#   默认 https://api.devnet.solana.com，突发请求会被限流（429），建议用专用 devnet RPC。
# 可选：SOLANA_FORWARDER_PROGRAM / SOLANA_FORWARDER_STATE（默认 devnet mock forwarder）
```

需要同时配置好 3.4 的 Privy（没有收款钱包就不能发奖，runner 启动时会报错）。启动日志应出现 `solana payouts: program ...`。

**行为说明**

- 启用后，merge 的 PR 在链上 campaign 不存在或被暂停时会直接 **failed**（`solana payout: ...`），不会在数据库里标为已结算。补建链上 campaign 后，在 GitHub 上重投那次 webhook 即可重试。
- 链上支付失败（余额不足、超过上限、已经付过）同样标为 failed，原因写在 error 里。
- 只有 merge 才会碰 Solana；预览从不建账户、不发交易。

**安全提示（devnet）**：mock forwarder 不验证 DON 签名，任何人都能通过它提交报告，每个不同的 PR ID 最多可被领走 `max_reward_per_pr`。devnet campaign 只放少量演示代币。上主网前要 `update-config` 到 keystone forwarder 并设置 `--workflow-owner`。

### 3.6 CRE 登录会话

推荐给 Fly **单独登录一次**，不要和你电脑上的会话共用 refresh token。两边轮流刷新同一个 token 时，可能互相把对方挤掉：

```bash
HOME=/tmp/cre-fly cre login                                   # 浏览器里完成登录
CRE_DIR=/tmp/cre-fly/.cre ./scripts/cre-login-env.sh | xargs fly secrets set --stage
rm -rf /tmp/cre-fly                                           # 直接删掉，不要 cre logout（logout 会吊销 Fly 正在用的会话）
```

偷懒的做法是直接用你电脑上的会话：`./scripts/cre-login-env.sh | xargs fly secrets set --stage`。

- 机器启动时，`CRE_LOGIN_YAML` / `CRE_CONTEXT_YAML` 会写进 `/home/app/.cre`。
- 这两个值等同于你 CRE 账号的密码，只放在 Fly secrets 里。
- 如果你有 `CRE_API_KEY`，可以设它代替以上步骤（优先级更高）。

检查暂存的 secrets（只显示名字和 digest）：

```bash
fly secrets list
```

## 4. GitHub App 设置

在 GitHub → Settings → Developer settings → GitHub Apps → 你的 App 里：

1. **Webhook URL**：`https://cre-runner.fly.dev/webhook`，**Webhook secret** 与 `GITHUB_WEBHOOK_SECRET` 一致，Active 勾上。
2. **Permissions → Repository**（只读即可，runner 只会申请这几项只读权限）：
   - Pull requests: Read
   - Contents: Read
   - Checks: Read
   - Metadata: Read
3. **Subscribe to events**：勾选 **Pull request**。其他事件也会入库，但只有 pull_request 触发执行。
4. **Install App** 到所有要参加 campaign 的仓库，或整个组织。

## 5. 部署

```bash
fly deploy
```

- 在 Fly 的远程 builder 上构建 `Dockerfile`。
- 下载 `cre` CLI，用 Go 1.25.3 预编译 workflow WASM，再编译 runner。
- 第一次构建要几分钟。
- 暂存的 secrets 会随这次部署一起生效。

## 6. 验证

1. **日志**：`fly logs`。启动时应该看到：

   ```
   INFO reviewer enabled llm=... model=...
   CRE executions enabled: workflow=test-workflow target=docker-simulation wasm="/app/cre/build/workflow.wasm" concurrency=2 instance=<machine id> cre_auth=login_session (seeded from CRE_LOGIN_YAML)
   ```

   - 出现 `CRE executions disabled`：GitHub App secrets 没设对。
   - `cre_auth=none`：CRE 会话没设对。

2. **健康检查**：`curl https://cre-runner.fly.dev/healthz` → `{"status":"ok"}`

3. **CRE 认证**：用 app 用户登录到机器上检查（root 的 HOME 不是 `/home/app`）：

   ```bash
   fly ssh console --user app -C "cre whoami"
   ```

4. **端到端**：
   1. 在前端创建 campaign：状态选 **Active**，填 reward、`max_reward_per_pr`、`min_score`，并添加要测试的仓库。
      也可以直接调 `POST /api/campaigns`，见 API.md 第 7 节。
   2. 在该仓库开一个 PR（不能是 draft）。
   3. 在 GitHub App → Advanced → Recent Deliveries 里，这次 `pull_request` 投递应该返回 `200 ack`。
   4. `fly logs` 里依次出现 `execution completed`（或 `execution failed` 加原因）。
   5. 在前端打开 campaign 页，右侧 **CRE executions** 选择仓库、填 PR 号，可以看到执行记录。
      执行中时页面每 3 秒自动刷新。也可以直接调 API：

      ```bash
      curl https://cre-runner.fly.dev/api/campaigns/<campaign id>/repos/<owner>/<repo>/prs/<number>/executions
      ```

   6. merge 这个 PR，会再出现一条 `event = merged` 的执行。
      符合条件时 `settled = true`。在 GitHub 上重投这次投递，只会多出一条 `skipped`。

## 7. 前端

前端部署环境需要设置：

| 变量 | 值 |
|------|----|
| `NEXT_PUBLIC_API_BASE_URL` | `https://cre-runner.fly.dev` |
| `NEXT_PUBLIC_PRIVY_APP_ID` | 和后端相同的 Privy App ID（不设则隐藏登录按钮） |
| `NEXT_PUBLIC_SOLANA_CLUSTER` | Solana Explorer 链接用的网络：`devnet`（默认）/ `mainnet-beta` |
| `NEXT_PUBLIC_SOLANA_RPC_URL` | 浏览器读链、模拟和发送交易用的 RPC（默认公共 devnet，会限流）。Helius 等 key 会暴露在浏览器里：在 Helius 后台把 key 限制到前端域名 |
| `NEXT_PUBLIC_CONTRIB_ORACLE_PROGRAM_ID` | 合约程序 ID，默认 `FSy2V61Tvm6bVHV4dGtoJS7T16eE7ZNjvGHEyT3aw6MA` |
| `NEXT_PUBLIC_USDC_MINT` | USDC campaign 默认的奖励 mint，默认 Circle devnet USDC `4zMMC9srt5Ri5X14GAgXhaHii3GnPAEERYPJgZJDncDU` |

前端域名要同时加进 `CORS_ALLOWED_ORIGINS`（后端）和 Privy Dashboard 的 Domains。

右上角 **Sign in** 用 GitHub 登录。登录后的 **My rewards**（`/me`）页面会显示：

- 钱包地址
- 你作为作者的全部执行记录和已结算奖励
- 一致性检查：runner 付款的钱包和你账号里的钱包不一致时会显示红色警告

campaign 详情页的 **Solana treasury** 区块用登录用户的 Privy Solana 钱包签名：

- **Create on Solana**：一笔交易里 `create_campaign` + `fund_campaign`，签名的钱包成为 sponsor。mint 的小数位必须和奖励币种一致（USDC 6 / SOL 9）；SOL campaign 用 wrapped SOL，注资时自动把 SOL 包装进去。成功后把 vault 地址写进 campaign 的 `treasury_address`。
- 已上链后显示状态、vault 余额、已付总额；sponsor 可以追加注资、暂停/恢复发奖。
- 每次签名前先模拟，失败直接显示程序日志，不弹钱包。钱包需要约 0.01 SOL 付租金和手续费（在 faucet.solana.com 给钱包地址领）。

## 8. 运行时行为

- **自动停机**：`fly.toml` 是 `auto_stop_machines = 'stop'`、`min_machines_running = 0`。没有流量时机器会停，下一个 webhook 会把它唤醒。
  - 停机时 runner 会等进行中的执行跑完，最多 `SHUTDOWN_GRACE_SECONDS`（默认 240 秒；`kill_timeout` 为 5 分钟）。
  - 被强杀遗留的执行，在下次启动时标为 `failed: interrupted: runner restarted`。
  - 想常驻就把 `min_machines_running` 改成 1。
- **CRE 会话过期**：access token 15 分钟有效，CLI 用 refresh token 自动续期，续期结果只存在机器文件系统里。
  机器重启后，会从 secret 里那份会话重新开始。
  如果执行开始报 CRE 认证错误（`Credential validation failed` 之类），重做第 3.6 步，然后 `fly deploy`（或 `fly secrets deploy`）。
- **可调参数**（`fly secrets set` 或 `fly.toml` 的 `[env]`）：
  - `MAX_CONCURRENCY`（默认 2）
  - `MAX_QUEUE`（默认 16）
  - `EVAL_TIMEOUT_SECONDS`（默认 120）
  - `LLM_TIMEOUT_SECONDS`（默认 120）
  - `SHUTDOWN_GRACE_SECONDS`（默认 240）

## 9. 排错

| 现象 | 原因 / 处理 |
|------|-------------|
| 没有任何执行记录 | campaign 不是 `active`、不在 `starts_at`–`ends_at` 时间窗内、仓库没加进 campaign、PR 是 draft、没设 `GITHUB_WEBHOOK_SECRET`、App 没订阅 Pull request 事件 |
| GitHub 投递返回 401 `invalid signature` | `GITHUB_WEBHOOK_SECRET` 与 App 设置不一致 |
| 执行 `failed`，error 含 `HTTP 401` / `HTTP 404` / `installation` | App 没装在该仓库，或缺少第 4 步的权限 |
| 执行 `failed`，error 含 CRE 认证错误 | 会话失效，重做第 3.6 步 |
| 执行 `failed: PR head moved` | 执行期间又 push 了新 commit，属正常；新 push 会触发新的执行 |
| 执行 `failed: runner busy` | 排队已满，调大 `MAX_QUEUE` / `MAX_CONCURRENCY`，或在 GitHub 上重投 |
| 执行 `failed: timed out` | 调大 `EVAL_TIMEOUT_SECONDS`；LLM 慢时调大 `LLM_TIMEOUT_SECONDS` |
| `fly logs` 出现 Out of memory | `fly scale memory 2048` |
| 前端报跨域错误 | `CORS_ALLOWED_ORIGINS` 没包含前端域名 |
