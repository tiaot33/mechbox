# Sources are typed

Every skill records the **Source** it came from. A Source is its own table row, one per origin, shared by every skill that came from it. A Source has one of four **types**: `github`, `url`, `store`, `local`. Type-specific locator data (git repo + ref, a URL, a store package id + version, a local path) lives in one flexible field (JSON) rather than a column or a table per type. A Source also records `last_fetched` and a `last_seen_ref` (git commit / store version / downloaded-file fingerprint) as Source-level fetch state; per-skill update decisions use the version-level `confirmed_commit` and the Source-level `http_validator` described below.

Skills arrive from several kinds of origin and v1 supports all four. Making the Source a first-class row lets **Collection** (skills grouped by Source) be pure-derived — no table of its own — and lets a **Self-developed skill** be expressed simply as a `local` Source. The four types carry differently-shaped locators, so a type tag plus a JSON locator avoids a column-per-type or a table-per-type; a new source kind is just a new enum value.

## Consequences

- **Collection** is not stored; it is a `GROUP BY source`.
- **Self-developed skill** is `source.type = local`.
- 各 Source 类型复用自身的抓取逻辑。`CheckUpdates(ctx, []SkillID)` 自行读取来源,不写入 Library;单项检查传一个 ID,统一返回逐项结果。GitHub 来源比较该 Skill 目录的 git tree SHA:相同则无更新、不取辅助文件;不同才视为有更新。无 SHA 的旧 GitHub 记录回退为比较文件内容及权限。URL / Store / local 仍比较文件内容及权限。`last_fetched` 与 `last_seen_ref` 记录抓取状态;GitHub 的 `last_seen_ref` 仍是仓库 commit,它们由同一来源下的 Skills 共享,不能单独用于判断某个 Skill 是否需要更新(版本级的判定见下文「版本级确认 commit」)。
- Updating a skill means re-fetching from its Source, producing a new version (see `0005`). GitHub 在目录 tree SHA 变化时才取文件并新增版本;SHA 未变可以刷新 `last_fetched` / `last_seen_ref`,但不新增版本。无 SHA 的旧记录在 Update 成功后补写 SHA(文件未变则只补写当前版本,不新增版本)。
- `Update` 只返回 Skill、Changed(#83 删除原有中文提示);GitHub 以 tree SHA 变化为准新增版本,其他来源仍以内容或权限变化为准。已激活副本与激活记录保持不变。

## GitHub 输入与 Git ref（#27）

- `SourceLocator.Branch` 保留字段名和 JSON 表达，语义扩展为 Git ref：分支跟踪分支头，tag 按其当前远端指向读取，完整 40 位 SHA 固定到该提交。分支/tag 的指向变化本身不构成 Skill 内容更新，须比较该 Skill 目录的 git tree SHA（无 SHA 的旧记录回退为比较文件和执行位）。
- `ResolveSource` 省略 ref 时查询远端默认分支并明确记录；失败不假定 main。显式 ref 不回退到其他 ref，完整 SHA 仅在正常分支/tag 抓取失败后尝试直接获取。
- 新解析输入按 GitHub owner/repo 与 ref 规范化，HTTPS/SSH 写法共享 Source/Collection；抓取使用本机 Git 认证，HTTPS 不可用时可对同一仓库/ref 尝试 SSH，均禁止交互提示。既有来源不批量合并或改写 SkillID。
- 子路径和名称是本次 Fetch 的选择条件，不进入 Source 身份。候选路径相对仓库根；后续更新按 Source 和 Skill 名重新发现。含斜杠的 ref 要显式传入，不猜测 tree URL 边界。
- skills.sh 仅为只读发现入口。搜索结果必须再经来源解析与 Fetch 验证，安装记录实际 GitHub Source，不作为 Store，也不新增持久表。skills.sh 的安全审计同为只读覆盖层：Fetch 对 GitHub 候选查一次并随会话暂存，只供展示；不入库、不参与 Sync、不作为安装门闩，查询失败只降级为状态，不影响 Fetch 成败（#56）。
- `CheckUpdates` 在单次调用内按已记录 Source（含 ref）复用读取结果；不同 ref 不合并。GitHub 来源只解析一次 commit，有 Skill 未被确认 commit 命中时才至多读取一次 tree（见下文 #62），各 Skill 用各自目录的 tree SHA 与当前版本比较；URL / Store / local 仍比较完整内容与执行位。它不写抓取记录、不新增版本、不重写 Activation；部分来源失败保留其他结果，取消时仅返回已完成项与 context 错误。之后的 Update 仍重新读取来源，但 GitHub 在 SHA 未变时只读取发现所需元数据,不得取辅助文件。

## 版本级确认 commit（#62）

- `last_seen_ref` 是 Source 级的,同一仓库下多个 Skill 共享,所以上文说它"不能单独用于判断某个 Skill 是否需要更新"。解决办法是把这个事实下沉到版本级:`skill_versions` 自 schema v6 起与 `git_tree_sha` 并列记录 `confirmed_commit`——该版本的目录 tree 最后一次被确认为远端最新时所在的 commit。两列一起构成一个版本的抓取记录:目录内容是什么、在哪个 commit 上确认过它是最新。它是"关于库内容的事实",随 Sync 走,不是缓存。
- 写入时机:`InstallFetch` 写入 Fetch 钉住的 commit;`Update` 无变化时刷新当前版本的确认 commit(补写 tree SHA 的旧记录同时补写),有变化时新版本直接带本次 commit。`CheckUpdates` 严格只读,不写它。
- 读取顺序:GitHub 检查与更新先发一次 `commits/{ref}`;所涉 Skill 的当前版本确认 commit 与之相同即无更新,不取 recursive tree。不同或为空(非 git 来源、尚未记录)才按原有逻辑取 tree 比较目录 SHA,无 SHA 时回退比较文件。`CheckUpdates` 内同一 Source 混合命中时至多取一次 tree,命中的 Skill 不参与比较。ref 为完整 SHA 的 Skill 因此永远只发一次请求且无更新;分支 force-push 或 tag 移动后 commit 变了,自然回到完整比较。
- `confirmed_commit` 自 schema v6 起存在;`Open` 把 v5 及更早的库一步升到当前版本,只加列不回填:Source 级的 `last_seen_ref` 对多 Skill 仓库不健全,空值即"未知,走完整比较",历史版本经一次 `Update` 补齐。`sources.last_seen_ref` 保留现有语义与写入时机,继续作为 Source 级展示信息。

## URL / Store 的 HTTP validator 与条件请求(#64)

- URL / Store 来源以前只为了"有没有更新"也要把整个压缩包(上限 64 MB)下完再比对。自 schema v7 起 `sources` 记录 `http_validator`——上次成功抓取时服务端给出的 validator 原文,优先 ETag(原样保留,包括弱 ETag 与不合规的无引号写法——服务端按自己的写法比对),其次 Last-Modified(只接受合法 HTTP-date,否则视为没给)。发请求时按值的形态区分:能解析为 HTTP-date 的填 `If-Modified-Since`,其余填 `If-None-Match`。它和 `last_seen_ref` 一样是 Source 级的"关于库内容的事实",随 Sync 走;GitHub / local 来源恒为空。
- 写入时机:`InstallFetch` 写入 Fetch 那次响应给出的 validator;`Update` 收到 200 时把新 validator 随 `last_seen_ref` 一起写回,收到 304 时不新增版本、只刷新 `last_fetched`,validator 与 `last_seen_ref` 保持。`CheckUpdates` 严格只读。
- 读取:`CheckUpdates` / `Update` 在 Source 记录了 validator 时带条件头;304 即无更新,正文零字节;200 则按原有流程整包下载比较。`CheckUpdates` 对同一 URL / Store Source 下多个 Skill 仍只发一次请求(记录了 validator 时带条件头),304 时整组无更新。`Fetch` 必须拿到内容,永远不带条件头。服务端不返回 validator 时列保持空、请求不带条件头,行为与之前完全一致;304 只在带了条件头的请求上有特殊含义,否则按普通非 2xx 错误处理。

- Fetch 与 GitHub 更新定位的候选发现对齐 vercel-labs/skills（#59）：三条来源路径共用约定目录优先、根短路、有界兜底、同名先到先得、必填 description 与文件名大小写不敏感规则；读取 Claude 清单扩展搜索，`PluginName` 仅供 Fetch 预览分组，不持久化。更新定位先匹配已发现候选的 tree SHA，再匹配名称；发现元数据在单次 Source 快照内复用。
