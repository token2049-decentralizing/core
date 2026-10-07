# cre-runner 部署到 Fly

一个 Fly app（`cre-runner`，区域 `sin`）跑全部功能：webhook 接收、前端 API、CRE workflow 执行（镜像内置 `cre` CLI 和预编译 WASM）、LLM reviewer。
以下命令都在 `cre-runner/` 目录执行（`fly.toml` 在这里）。

## 0. 前置条件

- `flyctl` 已安装并登录：`fly auth login`
- 本机有 `cre` CLI（`cre version`），用于生成 CRE 登录会话
- 手边有：Supabase 项目 URL 和 secret key、GitHub App（App ID、私钥 `github-app.pem`、webhook secret）、LLM API key 和 model
- 前端的线上域名（用于 CORS），例如 `https://cre-runner-frontend.vercel.app`

## 1. Supabase：建表

在 Supabase SQL Editor 里按顺序执行 `migrations/` 下还没执行过的文件。已有的库只需要新的这个：

```
migrations/003_cre_executions.sql
```

新库就从 `001_github_webhook_events.sql` 开始，按顺序全部执行。执行后，Table Editor 里应该能看到 `cre_executions` 表。

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

### 3.4 CRE 登录会话

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

前端部署环境设置 `NEXT_PUBLIC_API_BASE_URL=https://cre-runner.fly.dev`，前端域名要在 `CORS_ALLOWED_ORIGINS` 里。

## 8. 运行时行为

- **自动停机**：`fly.toml` 是 `auto_stop_machines = 'stop'`、`min_machines_running = 0`。没有流量时机器会停，下一个 webhook 会把它唤醒。
  - 停机时 runner 会等进行中的执行跑完，最多 `SHUTDOWN_GRACE_SECONDS`（默认 240 秒；`kill_timeout` 为 5 分钟）。
  - 被强杀遗留的执行，在下次启动时标为 `failed: interrupted: runner restarted`。
  - 想常驻就把 `min_machines_running` 改成 1。
- **CRE 会话过期**：access token 15 分钟有效，CLI 用 refresh token 自动续期，续期结果只存在机器文件系统里。
  机器重启后，会从 secret 里那份会话重新开始。
  如果执行开始报 CRE 认证错误（`Credential validation failed` 之类），重做第 3.4 步，然后 `fly deploy`（或 `fly secrets deploy`）。
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
| 执行 `failed`，error 含 CRE 认证错误 | 会话失效，重做第 3.4 步 |
| 执行 `failed: PR head moved` | 执行期间又 push 了新 commit，属正常；新 push 会触发新的执行 |
| 执行 `failed: runner busy` | 排队已满，调大 `MAX_QUEUE` / `MAX_CONCURRENCY`，或在 GitHub 上重投 |
| 执行 `failed: timed out` | 调大 `EVAL_TIMEOUT_SECONDS`；LLM 慢时调大 `LLM_TIMEOUT_SECONDS` |
| `fly logs` 出现 Out of memory | `fly scale memory 2048` |
| 前端报跨域错误 | `CORS_ALLOWED_ORIGINS` 没包含前端域名 |
