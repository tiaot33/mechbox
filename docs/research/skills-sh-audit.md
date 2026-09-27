# skills.sh 安全审计接口

**Date:** 2026-09-15  
**Question:** 调研 skills.sh 的安全审计接口 `/skills/audit/{source}/{slug}`。  
**Method:** 以官方 API 文档为合同，再用当日 live HTTP 核对路径、鉴权、状态码和字段；辅以 `skills` CLI 源码与 vercel-labs/skills 上关于审计过期的 issue。数值与样例以调研当日可见响应为准。

---

## 结论

用户说的 `/skills/audit/{source}/{slug}` **不是** JSON API。`https://skills.sh/skills/audit/...` 会 308 到 `www.skills.sh`，随后 HTML **404**。

真正的接口是：

```http
GET https://skills.sh/api/v1/skills/audit/{id}
```

`{id}` 与搜索结果里的稳定 `id` 相同，格式永远是 `{source}/{slug}`：

| 来源类型 | `id` 例子 | 实际 path |
| --- | --- | --- |
| GitHub | `vercel-labs/skills/find-skills` | `/api/v1/skills/audit/vercel-labs/skills/find-skills` |
| well-known | `mintlify.com/mintlify` | `/api/v1/skills/audit/mintlify.com/mintlify` |

官方写法是 `/api/v1/skills/audit/{source}/{skill}`，但 `{source}` 本身可含斜杠（`owner/repo`），路由是 catch-all：`x-matched-path: /api/v1/skills/audit/[...id]`。构造 URL 应把 `id` 按路径段拼接，**不要**把斜杠编码成 `%2F`。

对本仓库：现有 `SearchSkills` 已拿到同样的 `id`（`internal/library/search.go`），可直接拼 audit URL。ADR 0006 仍适用——skills.sh 只做只读发现，审计也是覆盖层，不应当成 Store，也不该新增持久权威表。

---

## 合同（官方文档）

来源：[API Reference](https://www.skills.sh/docs/api)

- Base URL：`https://skills.sh`；v1 全部在 `/api/v1/`，HTTPS，JSON。
- 目的：返回该 skill 在所有可用合作方上的安全审计（文档列举：Gen Agent Trust Hub、Socket、Snyk、Runlayer、ZeroLeaks）。
- 路径：用 listing / search 的 `id` 拼 `/api/v1/skills/audit/{id}`。
- 文档写「Authentication: Required」，示例 `curl` 却不带 `Authorization`。live 行为见下一节。
- 文档声明 404：还没有任何合作方审计过该 skill。审计在 skill **首次被安装后自动生成**，可能延迟几分钟。
- 站点安全说明（[Documentation](https://www.skills.sh/docs)）：定期审计恶意内容；问题报到 [security.vercel.com](https://security.vercel.com)。同时写明 **不保证** 目录里每个 skill 的质量或安全，安装前应自行审阅。

### 成功响应字段

顶层：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 稳定 ID，`{source}/{slug}` |
| `source` | string | GitHub 为 `owner/repo`，well-known 为域名 |
| `slug` | string | URL-safe skill slug |
| `audits` | array | 各合作方结果 |

`audits[]`：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `provider` | string | 展示名 |
| `slug` | string | 合作方 slug；站点详情页 `/{owner}/{repo}/{skill}/security/{slug}` |
| `status` | string | 归一化结论：`pass` / `warn` / `fail` |
| `summary` | string | 一行摘要 |
| `auditedAt` | string | ISO 8601 |
| `riskLevel` | string | 文档枚举：`NONE` / `LOW` / `MEDIUM` / `HIGH` / `CRITICAL`；多数合作方有 |
| `categories` | string[] | 仅 Agent Trust Hub |

统一错误形状：`{"error":"error_code","message":"..."}`。文档状态码：400 / 401 / 404 / 429 / 503。

---

## Live 核对（2026-09-15）

### 路径别名

| URL | 结果 |
| --- | --- |
| `/api/v1/skills/audit/vercel-labs/skills/find-skills` | **200** JSON |
| `www.skills.sh` 同一 path | 200，无额外跳转 |
| `/api/skills/audit/...`（无 `v1`） | HTML 404 |
| `/skills/audit/...` | 308 → `www` → HTML 404（`x-matched-path: /_not-found`） |
| `/{owner}/{repo}/{skill}/security/{provider}` | 站点页，200 HTML（`/[owner]/[repo]/[skill]/security/[provider]`） |

### 路径段规则

400 正文（live，文档未写这个 `error`）：

```json
{
  "error": "invalid_path",
  "message": "Expected format: /api/v1/skills/audit/{owner}/{repo}/{skill} or /api/v1/skills/audit/{domain}/{skill}"
}
```

实测：

| Path | HTTP | 解读 |
| --- | --- | --- |
| `/audit/vercel-labs`（1 段） | 400 | `invalid_path` |
| `/audit/vercel-labs/skills`（2 段） | 404 | 按 well-known `{domain}/{skill}` 解析，无审计 |
| `/audit/vercel-labs/skills/find-skills`（3 段） | 200 | GitHub |
| `/audit/vercel-labs/skills/find-skills/extra`（4 段） | 400 | `invalid_path` |
| `/audit/mintlify.com/mintlify`（2 段） | 200 | well-known |
| `/audit/mintlify.com/mintlify/extra`（3 段） | 404 | 被当成 GitHub `owner/repo/skill` |
| `.../audit/vercel-labs%2Fskills%2Ffind-skills` | 400 | 斜杠不可编码 |
| `.../audit/Vercel-Labs/skills/find-skills` | 200 | 大小写不敏感；响应 `id` 仍是小写 |

### 鉴权（文档与 live 不一致）

| 请求 | Audit | `/api/v1/skills` / `.../search` / 详情 `/api/v1/skills/{id}` | 本仓库在用的 `/api/search` |
| --- | --- | --- | --- |
| 无 `Authorization` | **200** | **401** `authentication_required` | **200**（`searchVersion: "legacy"`） |
| `Authorization: Bearer not-a-token` | **401** `invalid_token` | （未再测，预期同样失败） | — |

要点：

- Audit **可以匿名读**。文档写 Required，示例 curl 也不带 token；以 live 为准。
- **不要**顺手带一个无效 Bearer。匿名能过，坏 token 会 401。
- 401 正文提到两种凭据：Vercel OIDC JWT，或 `sk_live_...` API key。后者未出现在公开文档。
- 匿名成功响应 **没有** `X-RateLimit-*`。文档的 600/min 只描述已认证档。
- `OPTIONS` → 204，`Allow: GET, HEAD, OPTIONS`；`POST`/`PUT` → 405。未观察到 `Access-Control-Allow-Origin`，浏览器跨域调用不可靠；TUI / 服务端 GET 无此限制。

### 404

未知 skill 与「尚未审计」同一形状：

```json
{
  "error": "not_found",
  "message": "No security audits found for this skill. Audits are generated automatically after a skill is installed for the first time."
}
```

`open.feishu.cn/lark-doc`（排行榜上的 well-known）当日也是这条 404。不能用 404 区分「没有这个 skill」和「有 skill 但还没审计」。

成功响应 `Cache-Control: public`（无 `max-age`）；404 为 `public, max-age=0, must-revalidate`。文档说 listing/search 缓存 30–60s、详情/curated 5 分钟，**没有**单独写 audit 的 TTL。

---

## 当日样例

`GET /api/v1/skills/audit/vercel-labs/skills/find-skills`（合作方互相不一致）：

| provider | slug | status | riskLevel | 备注 |
| --- | --- | --- | --- | --- |
| Gen Agent Trust Hub | `agent-trust-hub` | pass | **SAFE** | 有 `categories`：`COMMAND_EXECUTION`, `EXTERNAL_DOWNLOADS`, `PROMPT_INJECTION` |
| Socket | `socket` | pass | （缺省） | summary: `No alerts` |
| Snyk | `snyk` | **warn** | MEDIUM | `Risk: MEDIUM · 1 issue`；`auditedAt` 带 `+00:00` 微秒 |
| Runlayer | `runlayer` | pass | NONE | |
| ZeroLeaks | `zeroleaks` | pass | NONE | `Score: 93/100` |

与文档的偏差：

- `riskLevel` live 出现 **`SAFE`**，不在文档五值枚举里。
- `categories` 文档例子是 `["NO_CODE","SAFE"]`；当日是风险类别，不是安全标签。
- Socket 常省略 `riskLevel`。
- `auditedAt` 有的是 `Z`，Snyk 是 `+00:00`。

`obra/superpowers/brainstorming`：Runlayer `fail` / `HIGH`（`3/8 files flagged`），其余 pass。同一 skill 的合作方结论可以相反。

`mintlify.com/mintlify`：只有 Socket 一条 pass。well-known 不一定五家齐。

---

## 另一条批量接口（CLI，非正式 v1）

`skills` CLI（[vercel-labs/skills `src/telemetry.ts`](https://github.com/vercel-labs/skills/blob/c5ad3a85/src/telemetry.ts)）走：

```http
GET https://add-skill.vercel.sh/audit?source={owner/repo}&skills={slug1,slug2}
```

超时 3s，失败返回 `null`，不挡安装。形状不同：

```json
{
  "find-skills": {
    "ath": { "risk": "safe", "analyzedAt": "..." },
    "socket": { "risk": "safe", "alerts": 0, "score": 90, "analyzedAt": "..." },
    "snyk": { "risk": "medium", "analyzedAt": "..." },
    "zeroleaks": { "risk": "safe", "score": 93, "analyzedAt": "..." }
  }
}
```

`risk` 为 `safe|low|medium|high|critical|unknown`。键是 `ath` 不是 `agent-trust-hub`。当日这份 **没有 Runlayer**。时间戳也和 v1 不完全对齐（Snyk 差一天）。不要把它和 `/api/v1/skills/audit` 当成同一合同。

---

## 审计新鲜度

合作方结果是 **快照**，不会随 GitHub 内容自动刷新：

- [vercel-labs/skills#1273](https://github.com/vercel-labs/skills/issues/1273)：skill 已改，Snyk 仍报已删除字符串；审计曾短暂 404 又回到旧 May 结果。
- [vercel-labs/skills#1896](https://github.com/vercel-labs/skills/issues/1896)：CLI 装到的是新版本，catalog/`/api/download` 与 Snyk 仍停在旧 hash。

`auditedAt` 只表示该次分析时间，不表示对应当前文件树。详情接口的 `hash` 本可对拍，但 `/api/v1/skills/{id}` **要鉴权**，本仓库现在拿不到。

---

## 对本仓库的含义

1. **接入成本低。** 匿名 GET，路径用现有 `SearchSkill.ID`。与 ADR 0006、现有 legacy `/api/search` 一致，都不必上 Vercel OIDC / `sk_live_`。
2. **不要迁到 v1 search/detail 除非先解决鉴权。** 那两条匿名 401；只有 legacy search 和这条 audit 目前能白嫖。
3. **404 是常态，不是传输失败。** 应与 HTTP 5xx / 超时分开。消息本身不能当「skill 不存在」。
4. **不要把 `status` 当入库门闩。** 合作方会打架；`riskLevel` 有文档外取值；审计会过期。最多做成搜索/入库前的提示，并链到 `https://skills.sh/{id}/security/{provider}`。
5. **解析要宽松。** 必填：`provider`, `slug`, `status`, `summary`, `auditedAt`。`riskLevel` / `categories` 可选。`status` 按文档三值处理，未知值降级而不是整包失败。
6. **URL 拼接：** `https://skills.sh/api/v1/skills/audit/` + 按 `/` 切开再 `PathEscape` **每一段**（与 `search.go` 对 `DetailURL` 的做法相同）。整段 `QueryEscape` / `%2F` 会 400。
7. **请求头：** 不要带无效 `Authorization`。
8. **well-known。** 当前搜索会把非 GitHub 来源打进 `Diagnostics`。即便以后做 audit，`open.feishu.cn/...` 这类也可能 404 或只有部分合作方。

未做：拿到 OIDC / `sk_live_` 后的配额头、429、以及详情 `hash` 与审计快照的对拍。
