# cre-runner 前端 API

Base URL：`https://cre-runner.fly.dev`（本地为 `http://localhost:8080`）

## 通用约定

- 所有接口返回 JSON。成功时数据放在 `data` 字段，失败时返回 `{"error": "<原因>"}`。
- 时间字段均为 ISO 8601 / RFC 3339 字符串（例如 `2026-10-06T17:20:11.123+00:00`）。
- CORS：环境变量 `CORS_ALLOWED_ORIGINS` 配置允许的前端 Origin（逗号分隔），为空时允许任意 Origin。
- 只记录真人用户触发的 webhook，bot（如 `dependabot[bot]`）触发的事件不会入库，因此也不会出现在下列接口中。
- 仓库名 `owner/repo` 直接放在路径里（例如 `/api/repos/octo-org/hello-world/events`）。

| # | 方法 | 路径 | 说明 |
|---|------|------|------|
| 1 | GET  | `/api/repos` | 列出所有收到过事件的仓库 |
| 2 | GET  | `/api/repos/{owner}/{repo}/filters` | 某个仓库可用的过滤项 |
| 3 | GET  | `/api/repos/{owner}/{repo}/events` | 某个仓库的事件列表（分页，按时间倒序） |
| 4 | GET  | `/api/deliveries/{delivery_id}` | 单个 delivery 的完整信息 |
| 5 | GET  | `/api/campaigns` | campaign 列表（分页，按创建时间倒序） |
| 6 | GET  | `/api/campaigns/{id}` | 单个 campaign 详情 |
| 7 | POST | `/api/campaigns` | 创建 reward campaign，可附带仓库 |
| 8 | GET  | `/api/campaigns/{id}/repos/{owner}/{repo}/prs/{number}/executions` | 某 campaign 下某仓库某 PR 的 CRE 执行记录 |

错误码：

| 状态码 | 含义 |
|--------|------|
| 400 | 参数不合法（`error` 中说明具体原因） |
| 404 | 仓库 / delivery / campaign 不存在 |
| 500 | 服务端或数据库错误 |

---

## 1. 列出所有仓库

`GET /api/repos`

无参数。按最近事件时间倒序返回。

**请求示例**

```bash
curl https://cre-runner.fly.dev/api/repos
```

**响应 200**

```json
{
  "data": [
    {
      "repository_full_name": "octo-org/hello-world",
      "event_count": 128,
      "last_event_at": "2026-10-06T17:20:11.123+00:00"
    },
    {
      "repository_full_name": "octo-org/docs",
      "event_count": 9,
      "last_event_at": "2026-10-05T08:01:44.002+00:00"
    }
  ]
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `repository_full_name` | string | 仓库全名 `owner/repo` |
| `event_count` | number | 该仓库累计事件数 |
| `last_event_at` | string | 最近一次事件的接收时间 |

---

## 2. 仓库可用过滤项

`GET /api/repos/{owner}/{repo}/filters`

返回该仓库出现过的 event、action、sender 及各自数量，用于前端渲染过滤器。返回的值可以直接作为接口 3 的查询参数。

**路径参数**

| 参数 | 说明 |
|------|------|
| `owner` | 仓库 owner |
| `repo` | 仓库名 |

**请求示例**

```bash
curl https://cre-runner.fly.dev/api/repos/octo-org/hello-world/filters
```

**响应 200**

```json
{
  "data": {
    "repository_full_name": "octo-org/hello-world",
    "total_events": 128,
    "events": [
      {
        "value": "pull_request",
        "count": 60,
        "actions": [
          { "value": "opened", "count": 25 },
          { "value": "synchronize", "count": 20 },
          { "value": "closed", "count": 15 }
        ]
      },
      { "value": "push", "count": 40, "actions": [] },
      {
        "value": "issue_comment",
        "count": 28,
        "actions": [{ "value": "created", "count": 28 }]
      }
    ],
    "actions": [
      { "value": "created", "count": 28 },
      { "value": "opened", "count": 25 },
      { "value": "synchronize", "count": 20 },
      { "value": "closed", "count": 15 }
    ],
    "senders": [
      { "login": "alice", "avatar_url": "https://avatars.githubusercontent.com/u/1?v=4", "count": 70 },
      { "login": "bob", "avatar_url": "https://avatars.githubusercontent.com/u/2?v=4", "count": 58 }
    ]
  }
}
```

| 字段 | 说明 |
|------|------|
| `total_events` | 该仓库事件总数 |
| `events[]` | 事件类型（`X-GitHub-Event`），附带该类型下出现过的 `actions`，适合做级联过滤 |
| `actions[]` | 所有 action 汇总（不区分事件类型） |
| `senders[]` | 触发事件的 GitHub 用户及头像 |

所有列表都按 `count` 倒序排列。

**响应 404**：仓库没有任何事件。

```json
{ "error": "repository not found" }
```

---

## 3. 仓库事件列表

`GET /api/repos/{owner}/{repo}/events`

按接收时间从最近到最远排序，分页返回。

**查询参数**

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `page` | int | `0` | 页码，从 0 开始 |
| `page_size` | int | `20` | 每页条数，1–100 |
| `event` | string | – | 按事件类型过滤，如 `pull_request` |
| `action` | string | – | 按 action 过滤，如 `opened` |
| `sender` | string | – | 按触发者 login 过滤 |
| `number` | int | – | 按 PR / issue 编号过滤 |

过滤条件之间为 AND 关系。

**请求示例**

```bash
# 默认：最近 20 条
curl https://cre-runner.fly.dev/api/repos/octo-org/hello-world/events

# 第 2 页，alice 打开的 PR
curl "https://cre-runner.fly.dev/api/repos/octo-org/hello-world/events?page=1&event=pull_request&action=opened&sender=alice"

# PR #42 的所有相关事件（PR 本身、评论、review 等）
curl "https://cre-runner.fly.dev/api/repos/octo-org/hello-world/events?number=42"
```

**响应 200**

```json
{
  "data": [
    {
      "id": 1042,
      "delivery_id": "2f1459e0-c1aa-11f1-9a98-b70712a5a439",
      "event": "pull_request",
      "action": "opened",
      "number": 42,
      "sender": {
        "login": "alice",
        "avatar_url": "https://avatars.githubusercontent.com/u/1?v=4"
      },
      "subject": {
        "type": "pull_request",
        "number": 42,
        "title": "Fix race in scheduler",
        "state": "open",
        "html_url": "https://github.com/octo-org/hello-world/pull/42",
        "merged": false
      },
      "ref": null,
      "received_at": "2026-10-06T17:20:11.123+00:00"
    },
    {
      "id": 1041,
      "delivery_id": "1a2b3c4d-c1a9-11f1-8f00-0242ac120002",
      "event": "issue_comment",
      "action": "created",
      "number": 41,
      "sender": { "login": "bob", "avatar_url": "https://avatars.githubusercontent.com/u/2?v=4" },
      "subject": {
        "type": "issue",
        "number": 41,
        "title": "Scheduler drops jobs under load",
        "state": "open",
        "html_url": "https://github.com/octo-org/hello-world/issues/41"
      },
      "ref": null,
      "received_at": "2026-10-06T16:02:37.551+00:00"
    },
    {
      "id": 1040,
      "delivery_id": "0f0e0d0c-c1a8-11f1-8f00-0242ac120002",
      "event": "push",
      "action": null,
      "number": null,
      "sender": { "login": "alice", "avatar_url": "https://avatars.githubusercontent.com/u/1?v=4" },
      "subject": null,
      "ref": "refs/heads/main",
      "received_at": "2026-10-06T15:40:02.010+00:00"
    }
  ],
  "pagination": {
    "page": 0,
    "page_size": 20,
    "total": 128,
    "has_more": true
  }
}
```

**事件字段**

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | number | 记录 ID（每次接收一行） |
| `delivery_id` | string | GitHub delivery ID，用于接口 4 |
| `event` | string | 事件类型 |
| `action` | string \| null | 事件 action，部分事件（如 `push`）没有 |
| `number` | number \| null | 关联的 PR / issue 编号 |
| `sender` | object \| null | 触发者 `{login, avatar_url}` |
| `subject` | object \| null | 关联的 PR / issue，见下表；没有关联时为 `null` |
| `ref` | string \| null | `push`、`create`、`delete` 等事件的 git ref |
| `received_at` | string | 接收时间 |

**`subject` 字段**

| 字段 | 类型 | 说明 |
|------|------|------|
| `type` | string | `pull_request` 或 `issue`。PR 下的评论（`issue_comment`）也会标为 `pull_request` |
| `number` | number | PR / issue 编号 |
| `title` | string | 标题 |
| `state` | string | `open` / `closed` |
| `html_url` | string | GitHub 页面链接 |
| `merged` | boolean | 仅 `pull_request` 事件返回，是否已合并 |

**`pagination` 字段**

| 字段 | 说明 |
|------|------|
| `page` / `page_size` | 本次请求的分页参数 |
| `total` | 满足过滤条件的总条数 |
| `has_more` | 是否还有下一页 |

页码超出范围时返回空 `data`，`total` 仍为实际总数。

**响应 400**

```json
{ "error": "page_size must be an integer between 1 and 100" }
```

---

## 4. 获取单个 delivery

`GET /api/deliveries/{delivery_id}`

返回该 delivery 的全部信息，包括完整的 GitHub `payload` 和请求头。GitHub 重新投递时会复用同一个 `delivery_id`，此时返回最近一次接收的记录。

`payload` 结构取决于事件类型，参考 [GitHub webhook 文档](https://docs.github.com/en/webhooks/webhook-events-and-payloads)。

**请求示例**

```bash
curl https://cre-runner.fly.dev/api/deliveries/2f1459e0-c1aa-11f1-9a98-b70712a5a439
```

**响应 200**

```json
{
  "data": {
    "id": 1042,
    "delivery_id": "2f1459e0-c1aa-11f1-9a98-b70712a5a439",
    "event": "pull_request",
    "action": "opened",
    "number": 42,
    "hook_id": 512345678,
    "installation_id": 87654321,
    "repository_full_name": "octo-org/hello-world",
    "sender_login": "alice",
    "signature_valid": true,
    "headers": {
      "content-type": "application/json",
      "user-agent": "GitHub-Hookshot/abc1234",
      "x-github-delivery": "2f1459e0-c1aa-11f1-9a98-b70712a5a439",
      "x-github-event": "pull_request",
      "x-github-hook-id": "512345678",
      "x-github-hook-installation-target-id": "1234567",
      "x-github-hook-installation-target-type": "integration",
      "x-hub-signature-256": "sha256=..."
    },
    "payload": {
      "action": "opened",
      "number": 42,
      "pull_request": {
        "number": 42,
        "title": "Fix race in scheduler",
        "state": "open",
        "html_url": "https://github.com/octo-org/hello-world/pull/42",
        "merged": false,
        "user": { "login": "alice" },
        "...": "..."
      },
      "repository": { "full_name": "octo-org/hello-world", "...": "..." },
      "sender": { "login": "alice", "type": "User", "...": "..." },
      "installation": { "id": 87654321 }
    },
    "received_at": "2026-10-06T17:20:11.123+00:00"
  }
}
```

| 字段 | 说明 |
|------|------|
| `hook_id` | GitHub webhook ID |
| `installation_id` | GitHub App installation ID |
| `signature_valid` | webhook 签名是否校验通过；服务端未配置 secret 时为 `null` |
| `headers` | GitHub 相关请求头（小写 key） |
| `payload` | GitHub 原始 payload |

其余字段同接口 3。

**响应 404**

```json
{ "error": "delivery not found" }
```

---

## 5. campaign 列表

`GET /api/campaigns`

按创建时间从新到旧分页返回 campaign，每个 campaign 附带它 attach 的全部仓库。

**查询参数**

| 参数 | 类型 | 默认 | 说明 |
|------|------|------|------|
| `page` | int | `0` | 页码，从 0 开始 |
| `page_size` | int | `20` | 每页条数，1–100 |
| `status` | string | – | 按状态过滤：`draft` / `active` / `paused` / `ended` |
| `repo` | string | – | 只返回 attach 了该仓库（`owner/repo`）的 campaign |

过滤条件之间为 AND 关系。按 `repo` 过滤时，返回的 `repos` 仍是该 campaign 的完整仓库列表。

**请求示例**

```bash
# 默认：最新 20 个
curl https://cre-runner.fly.dev/api/campaigns

# 某个仓库参与的进行中 campaign
curl "https://cre-runner.fly.dev/api/campaigns?status=active&repo=octo-org/hello-world"
```

**响应 200**

```json
{
  "data": [
    {
      "id": "6f1c2a9e-3b7d-4c1e-9a55-0d2f8b7e4c11",
      "name": "CodeRabbit OSS Challenge",
      "description": "Reward merged PRs that fix linked issues.",
      "sponsor": "Example Developer Tool",
      "reward_asset": "USDC",
      "budget": 10000.000000,
      "max_reward_per_pr": 500.000000,
      "min_score": 70,
      "eligibility": { "merged": true, "ci_passed": true, "linked_issue": true, "duplicate": false },
      "scoring": { "issue_relevance": 20, "correctness": 25, "tests": 15, "code_quality": 15, "maintainer_review": 15, "novelty": 10 },
      "status": "active",
      "treasury_address": null,
      "starts_at": "2026-10-10T00:00:00+00:00",
      "ends_at": "2026-12-31T23:59:59+00:00",
      "created_at": "2026-10-07T09:12:33.456+00:00",
      "repos": ["octo-org/docs", "octo-org/hello-world"]
    }
  ],
  "pagination": {
    "page": 0,
    "page_size": 20,
    "total": 1,
    "has_more": false
  }
}
```

campaign 字段含义见接口 7 的请求体和响应说明；`repos` 按字母序排列。`pagination` 含义同接口 3，页码超出范围时返回空 `data`。

**响应 400 示例**

```json
{ "error": "status must be one of draft, active, paused, ended" }
```

```json
{ "error": "repo must be owner/repo" }
```

---

## 6. 获取单个 campaign

`GET /api/campaigns/{id}`

返回单个 campaign 及其 attach 的全部仓库。

**路径参数**

| 参数 | 说明 |
|------|------|
| `id` | campaign UUID（创建时返回的 `id`） |

**请求示例**

```bash
curl https://cre-runner.fly.dev/api/campaigns/6f1c2a9e-3b7d-4c1e-9a55-0d2f8b7e4c11
```

**响应 200**

```json
{
  "data": {
    "id": "6f1c2a9e-3b7d-4c1e-9a55-0d2f8b7e4c11",
    "name": "CodeRabbit OSS Challenge",
    "description": "Reward merged PRs that fix linked issues.",
    "sponsor": "Example Developer Tool",
    "reward_asset": "USDC",
    "budget": 10000.000000,
    "max_reward_per_pr": 500.000000,
    "min_score": 70,
    "eligibility": { "merged": true, "ci_passed": true, "linked_issue": true, "duplicate": false },
    "scoring": { "issue_relevance": 20, "correctness": 25, "tests": 15, "code_quality": 15, "maintainer_review": 15, "novelty": 10 },
    "status": "active",
    "treasury_address": null,
    "starts_at": "2026-10-10T00:00:00+00:00",
    "ends_at": "2026-12-31T23:59:59+00:00",
    "created_at": "2026-10-07T09:12:33.456+00:00",
    "repos": ["octo-org/docs", "octo-org/hello-world"]
  }
}
```

字段含义见接口 7 的请求体和响应说明；`repos` 按字母序排列。

**响应 404**：campaign 不存在，或 `id` 不是合法 UUID。

```json
{ "error": "campaign not found" }
```

---

## 7. 创建 campaign

`POST /api/campaigns`

创建一个 PR reward campaign，可选同时 attach 若干仓库。`Content-Type: application/json`。

**请求体**

| 字段 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `name` | string | 是 | – | campaign 名称，不能为空 |
| `description` | string | 否 | `null` | 描述 |
| `sponsor` | string | 否 | `null` | 赞助方 |
| `reward_asset` | string | 否 | `"USDC"` | `"USDC"` 或 `"SOL"` |
| `budget` | number \| string | 是 | – | 总预算，> 0，最多 6 位小数 |
| `max_reward_per_pr` | number \| string | 是 | – | 单个 PR 最高奖励，> 0 且 ≤ `budget` |
| `min_score` | int | 否 | `null` | 最低贡献分，0–100 |
| `eligibility` | object | 否 | `{}` | 资格规则，自由结构 |
| `scoring` | object | 否 | `{}` | 评分权重，自由结构 |
| `status` | string | 否 | `"draft"` | `"draft"` 或 `"active"` |
| `starts_at` | string | 否 | `null` | 开始时间（RFC 3339） |
| `ends_at` | string | 否 | `null` | 结束时间，必须晚于 `starts_at` |
| `repos` | string[] | 否 | `[]` | attach 的仓库 `owner/repo`，最多 50 个，重复项自动去重 |

金额建议以字符串传递（如 `"10000.50"`），避免浏览器浮点误差。不认识的字段会被拒绝（400），方便发现拼写错误。

**请求示例**

```bash
curl -X POST https://cre-runner.fly.dev/api/campaigns \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "CodeRabbit OSS Challenge",
    "description": "Reward merged PRs that fix linked issues.",
    "sponsor": "Example Developer Tool",
    "reward_asset": "USDC",
    "budget": "10000",
    "max_reward_per_pr": "500",
    "min_score": 70,
    "eligibility": {
      "merged": true,
      "ci_passed": true,
      "linked_issue": true,
      "duplicate": false
    },
    "scoring": {
      "issue_relevance": 20,
      "correctness": 25,
      "tests": 15,
      "code_quality": 15,
      "maintainer_review": 15,
      "novelty": 10
    },
    "status": "active",
    "starts_at": "2026-10-10T00:00:00Z",
    "ends_at": "2026-12-31T23:59:59Z",
    "repos": ["octo-org/hello-world", "octo-org/docs"]
  }'
```

**响应 201**

```json
{
  "data": {
    "id": "6f1c2a9e-3b7d-4c1e-9a55-0d2f8b7e4c11",
    "name": "CodeRabbit OSS Challenge",
    "description": "Reward merged PRs that fix linked issues.",
    "sponsor": "Example Developer Tool",
    "reward_asset": "USDC",
    "budget": 10000.000000,
    "max_reward_per_pr": 500.000000,
    "min_score": 70,
    "eligibility": { "merged": true, "ci_passed": true, "linked_issue": true, "duplicate": false },
    "scoring": {
      "issue_relevance": 20,
      "correctness": 25,
      "tests": 15,
      "code_quality": 15,
      "maintainer_review": 15,
      "novelty": 10
    },
    "status": "active",
    "treasury_address": null,
    "starts_at": "2026-10-10T00:00:00+00:00",
    "ends_at": "2026-12-31T23:59:59+00:00",
    "created_at": "2026-10-07T09:12:33.456+00:00",
    "repos": ["octo-org/hello-world", "octo-org/docs"]
  }
}
```

| 字段 | 说明 |
|------|------|
| `id` | campaign UUID |
| `treasury_address` | Solana campaign treasury 地址，资金到位后由后续流程写入，创建时为 `null` |
| `created_at` | 创建时间 |
| `repos` | 已 attach 的仓库 |

其余字段与请求体一致。

**响应 400 示例**

```json
{ "error": "max_reward_per_pr must not exceed budget" }
```

```json
{ "error": "invalid repo \"hello-world\", expected owner/repo" }
```

```json
{ "error": "invalid JSON body: json: unknown field \"reward\"" }
```

---

## 8. PR 的 CRE 执行记录

`GET /api/campaigns/{id}/repos/{owner}/{repo}/prs/{number}/executions`

某个 campaign 下、某个仓库、某个 PR 的全部 CRE workflow 执行，按创建时间倒序（最新在前），分页。
执行由 `pull_request` webhook 异步触发，状态会随时间变化，执行中（`queued` / `running`）的记录可轮询刷新。

| 参数 | 位置 | 说明 |
|------|------|------|
| `id` | path | campaign UUID |
| `owner` / `repo` | path | 仓库 |
| `number` | path | PR 编号，正整数 |
| `page` | query | 页码，从 0 开始，默认 0 |
| `page_size` | query | 每页条数，1–100，默认 20 |

**请求示例**

```bash
curl "https://cre-runner.fly.dev/api/campaigns/1f0c6a52-0d0e-4b39-9a7e-2d6c1f4b8e11/repos/octo-org/hello-world/prs/42/executions"
```

**响应示例**

```json
{
  "data": [
    {
      "id": "6b1e2c1a-5a3f-4f7e-9a51-0f2a9e7c3d10",
      "delivery_id": "3c9a5f10-a2d1-11f0-8f6e-2b1d6f0e9a44",
      "campaign_id": "1f0c6a52-0d0e-4b39-9a7e-2d6c1f4b8e11",
      "repository_full_name": "octo-org/hello-world",
      "pr_number": 42,
      "event": "merged",
      "head_sha": "9f2b6c1d0e8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c",
      "status": "completed",
      "score": 89,
      "eligible": true,
      "reward": "445000000",
      "evaluation_hash": "0x4ea2c445…",
      "policy_hash": "0xbb5dc2d2…",
      "settled": true,
      "error": null,
      "created_at": "2026-10-07T09:12:03.120+00:00",
      "started_at": "2026-10-07T09:12:03.410+00:00",
      "finished_at": "2026-10-07T09:13:41.002+00:00"
    },
    {
      "id": "a7d0c3e2-1b4f-4c8a-8e2d-3f5a6b7c8d90",
      "delivery_id": "0e1f2a30-a2c9-11f0-9b1c-7a2e4d6f8b12",
      "campaign_id": "1f0c6a52-0d0e-4b39-9a7e-2d6c1f4b8e11",
      "repository_full_name": "octo-org/hello-world",
      "pr_number": 42,
      "event": "opened",
      "head_sha": "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b",
      "status": "failed",
      "score": null,
      "eligible": null,
      "reward": null,
      "evaluation_hash": null,
      "policy_hash": null,
      "settled": false,
      "error": "PR head moved: requested 1a2b3c4…, current 9f2b6c1…",
      "created_at": "2026-10-07T08:55:40.000+00:00",
      "started_at": "2026-10-07T08:55:40.300+00:00",
      "finished_at": "2026-10-07T08:55:42.100+00:00"
    }
  ],
  "pagination": { "page": 0, "page_size": 20, "total": 2, "has_more": false }
}
```

| 字段 | 说明 |
|------|------|
| `id` | CRE execution id |
| `delivery_id` | 触发它的 webhook，可用接口 4 查看 |
| `event` | `opened`（预览，不发奖励）/ `merged`（结算） |
| `status` | `queued` / `running` / `completed` / `failed` / `skipped`，见下文 |
| `score` / `eligible` / `reward` / `evaluation_hash` / `policy_hash` | 仅 `completed` 时有值 |
| `reward` | token 最小单位的字符串（USDC 6 位小数：`"445000000"` = 445 USDC；SOL 9 位），不要用浮点数解析 |
| `settled` | 是否为该 PR 的结算记录（每个 campaign + 仓库 + PR 最多一条） |
| `error` | `failed` 的失败原因，或 `skipped` 的原因 |
| `started_at` / `finished_at` | 开始运行 / 结束时间，未到该阶段为 `null` |

PR 没有执行记录时返回空数组。campaign 不存在返回 404；`repo`、`number`、分页参数不合法返回 400。

---

## 数据库

表结构见 `migrations/`：

- `001_github_webhook_events.sql`：webhook 事件表
- `002_frontend_api.sql`：`number` 生成列、`github_webhook_repos` / `github_webhook_repo_facets` 视图、`campaigns` / `campaign_repos` 表
- `003_cre_executions.sql`：CRE workflow 执行记录表 `cre_executions`（见下文）

---

## CRE 执行（webhook 触发，异步）

旧的 `POST /evaluate` 已移除。`POST /webhook` 收到 `pull_request` 事件后先同步写入 `github_webhook_events` 并立即返回 `ack`，
然后在 goroutine 里对该仓库所属的每个**进行中** campaign（`status = 'active'` 且在 `starts_at` / `ends_at` 时间窗内）各跑一次 CRE workflow：

| PR action | workflow `event` |
|-----------|------------------|
| `opened` / `reopened` / `synchronize` / `ready_for_review`（draft 跳过） | `opened`：预览，不发奖励 |
| `closed` 且 `merged = true` | `merged`：结算 |

每次执行写一行 `cre_executions`，`id` 即 CRE execution id（runner 生成的 UUID），状态依次更新：

| status | 含义 |
|--------|------|
| `queued` | 已入库，等待执行槽位（`MAX_CONCURRENCY` 个并发，最多 `MAX_QUEUE` 个排队） |
| `running` | 正在预热 LLM reviewer 并运行 `cre workflow simulate`（`started_at`） |
| `completed` | 成功：`score` / `eligible` / `reward` / `evaluation_hash` / `policy_hash` 已写入 |
| `failed` | 失败，原因见 `error`（PR 不存在、head 已变、LLM 错误、超时、队列已满、runner 重启……） |
| `skipped` | `merged` 已被之前的执行结算过（`error` 中有那次执行的 id） |

- **只结算一次**：`merged` 且 `eligible` 且 `reward != "0"` 的结果会设 `settled = true`；
  唯一索引保证每个 (campaign, repo, PR) 最多一条 `settled` 记录，重复投递只会得到 `skipped`。
- workflow 配置按 campaign 生成：`reward_asset`（USDC 6 位 / SOL 9 位小数）、`max_reward_per_pr`（score_based 封顶）、
  `min_score`、`eligibility.merged / ci_passed / linked_issue`。`scoring` 暂未映射，权重固定为 evidence 40% / code reviewer 30% / LLM 30%。
- 停机（SIGINT/SIGTERM）时等待进行中的执行最多 `SHUTDOWN_GRACE_SECONDS` 秒，其余记为 `failed`；
  被强杀遗留的 `queued` / `running` 行会在同一台机器（`runner_instance`）下次启动时标记为 `failed`。
