# Sources are typed

Every skill records the **Source** it came from. A Source is its own table row, one per origin, shared by every skill that came from it. A Source has one of four **types**: `github`, `url`, `store`, `local`. Type-specific locator data (git repo + ref, a URL, a store package id + version, a local path) lives in one flexible field (JSON) rather than a column or a table per type. A Source also records `last_fetched` and a `last_seen_ref` (git commit / store version / downloaded-file fingerprint) for update checks.

Skills arrive from several kinds of origin and v1 supports all four. Making the Source a first-class row lets **Collection** (skills grouped by Source) be pure-derived — no table of its own — and lets a **Self-developed skill** be expressed simply as a `local` Source. The four types carry differently-shaped locators, so a type tag plus a JSON locator avoids a column-per-type or a table-per-type; a new source kind is just a new enum value.

## Consequences

- **Collection** is not stored; it is a `GROUP BY source`.
- **Self-developed skill** is `source.type = local`.
- 各 Source 类型复用自身的抓取逻辑。#25 的 `CheckUpdate(ctx, SkillID)` 自行读取来源,不写入 Library。GitHub 来源比较该 Skill 目录的 git tree SHA:相同则无更新、不取文件;不同才视为有更新。无 SHA 的旧 GitHub 记录回退为比较文件内容及权限。URL / Store / local 仍比较文件内容及权限。`last_fetched` 与 `last_seen_ref` 记录抓取状态;GitHub 的 `last_seen_ref` 仍是仓库 commit,它们由同一来源下的 Skills 共享,不能单独用于判断某个 Skill 是否需要更新。
- Updating a skill means re-fetching from its Source, producing a new version (see `0005`). GitHub 在目录 tree SHA 变化时才取文件并新增版本;SHA 未变可以刷新 `last_fetched` / `last_seen_ref`,但不新增版本。无 SHA 的旧记录在 Update 成功后补写 SHA(文件未变则只补写当前版本,不新增版本)。
- #25 的 `Update` 返回 Skill、Changed 和激活副本可能滞后的提示;GitHub 以 tree SHA 变化为准新增版本,其他来源仍以内容或权限变化为准。已激活副本与激活记录保持不变。

## GitHub 输入与 Git ref（#27）

- `SourceLocator.Branch` 保留字段名和 JSON 表达，语义扩展为 Git ref：分支跟踪分支头，tag 按其当前远端指向读取，完整 40 位 SHA 固定到该提交。分支/tag 的指向变化本身不构成 Skill 内容更新，须比较该 Skill 目录的 git tree SHA（无 SHA 的旧记录回退为比较文件和执行位）。
- `ResolveSource` 省略 ref 时查询远端默认分支并明确记录；失败不假定 main。显式 ref 不回退到其他 ref，完整 SHA 仅在正常分支/tag 抓取失败后尝试直接获取。
- 新解析输入按 GitHub owner/repo 与 ref 规范化，HTTPS/SSH 写法共享 Source/Collection；抓取使用本机 Git 认证，HTTPS 不可用时可对同一仓库/ref 尝试 SSH，均禁止交互提示。既有来源不批量合并或改写 SkillID。
- 子路径和名称是本次 Fetch 的选择条件，不进入 Source 身份。候选路径相对仓库根；后续更新按 Source 和 Skill 名重新发现。含斜杠的 ref 要显式传入，不猜测 tree URL 边界。
- skills.sh 仅为只读发现入口。搜索结果必须再经来源解析与 Fetch 验证，安装记录实际 GitHub Source，不作为 Store，也不新增持久表。skills.sh 的安全审计同为只读覆盖层：Fetch 对 GitHub 候选查一次并随会话暂存，只供展示；不入库、不参与 Sync、不作为安装门闩，查询失败只降级为状态，不影响 Fetch 成败（#56）。
- `CheckUpdates` 在单次调用内按已记录 Source（含 ref）复用读取结果；不同 ref 不合并。GitHub 来源只远程读取一次 tree，各 Skill 用各自目录的 tree SHA 与当前版本比较；URL / Store / local 仍比较完整内容与执行位。它不写抓取记录、不新增版本、不重写 Activation；部分来源失败保留其他结果，取消时仅返回已完成项与 context 错误。之后的 Update 仍重新读取来源，但 GitHub 在 SHA 未变时不得取文件。
