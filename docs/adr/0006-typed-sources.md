# Sources are typed

Every skill records the **Source** it came from. A Source is its own table row, one per origin, shared by every skill that came from it. A Source has one of four **types**: `github`, `url`, `store`, `local`. Type-specific locator data (git repo + branch, a URL, a store package id + version, a local path) lives in one flexible field (JSON) rather than a column or a table per type. A Source also records `last_fetched` and a `last_seen_ref` (git commit / store version / downloaded-file fingerprint) for update checks.

Skills arrive from several kinds of origin and v1 supports all four. Making the Source a first-class row lets **Collection** (skills grouped by Source) be pure-derived — no table of its own — and lets a **Self-developed skill** be expressed simply as a `local` Source. The four types carry differently-shaped locators, so a type tag plus a JSON locator avoids a column-per-type or a table-per-type; a new source kind is just a new enum value.

## Consequences

- **Collection** is not stored; it is a `GROUP BY source`.
- **Self-developed skill** is `source.type = local`.
- Each type has its own "check for updates / fetch new version" logic, but all of it compares against the same two `sources` fields (`last_fetched`, `last_seen_ref`).
- Updating a skill means re-fetching from its Source, producing a new version (see `0005`).
