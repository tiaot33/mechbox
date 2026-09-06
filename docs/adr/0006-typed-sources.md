# Sources are typed

Every skill records the **Source** it came from. A Source is its own table row, one per origin, shared by every skill that came from it. A Source has one of four **types**: `github`, `url`, `store`, `local`. Type-specific locator data (git repo + branch, a URL, a store package id + version, a local path) lives in one flexible field (JSON) rather than a column or a table per type. A Source also records `last_fetched` and a `last_seen_ref` (git commit / store version / downloaded-file fingerprint) for update checks.

Skills arrive from several kinds of origin and v1 supports all four. Making the Source a first-class row lets **Collection** (skills grouped by Source) be pure-derived — no table of its own — and lets a **Self-developed skill** be expressed simply as a `local` Source. The four types carry differently-shaped locators, so a type tag plus a JSON locator avoids a column-per-type or a table-per-type; a new source kind is just a new enum value.

## Consequences

- **Collection** is not stored; it is a `GROUP BY source`.
- **Self-developed skill** is `source.type = local`.
- 各 Source 类型复用自身的抓取逻辑。#25 的 `CheckUpdate(ctx, SkillID)` 自行读取来源,比较目标 Skill 的文件内容及权限,不写入 Library。`last_fetched` 与 `last_seen_ref` 记录抓取状态;它们由同一来源下的 Skills 共享,不能单独用于判断某个 Skill 是否需要更新。
- Updating a skill means re-fetching from its Source, producing a new version (see `0005`).
- #25 的 `Update` 返回 Skill、Changed 和激活副本可能滞后的提示;只有内容或权限变化才新增版本,已激活副本与激活记录保持不变。
