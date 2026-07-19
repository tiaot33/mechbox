# Skill history is content-addressed

Every skill keeps its full version history. A **version** is a snapshot of the skill's entire file set at one point in time.

Blobs are stored **per file, not per directory**. Each file's content goes into a `blobs` table keyed by a content fingerprint (hash), so identical content is stored **once** for the whole Library. A version is a list of `(path, blob-fingerprint, mode)` rows in `version_files`, where `path` is the file's full path relative to the skill root (e.g. `scripts/helper.py`). Directory structure is **implied by those paths** — there is no directory table and no per-directory tree object (git-style layered trees are overkill at skill scale). `mode` records whether the file is regular or executable, so activation can restore the executable bit (e.g. `scripts/helper.py`).

Per-file blobs are what make both **dedup and diff** work. A whole-directory blob would change its fingerprint on any edit (killing dedup) and would only tell you *that* a version changed, not *which* file (killing cheap diff). Diffing two versions is just comparing their two `version_files` lists: same path + different fingerprint = changed; path only in the old = deleted; path only in the new = added.

History is a core value of the tool, not an add-on. Because file contents live in the Library (see `0001`) and the whole Library is synced, history must be the Library's own data — it cannot lean on a remote git, and **self-developed skills have no remote at all**. Naively copying every file on every version would bloat the Library on each tiny edit and slow Sync; content-addressing means keeping full history costs only the *new* content.

## Consequences

- Installing, updating, or editing a skill fingerprints each file; only new fingerprints are written to `blobs`.
- A version is one `skill_versions` row plus its `version_files` rows (`path`, `fingerprint`, `mode`), where `path` is relative to the skill root and directories are implied.
- The `skills` row carries a **current version** pointer.
- A blob is shared across versions, so it is only reclaimable when no `version_files` row points at it (future GC, to be specified).
- Empty directories cannot be represented (no file to hang them on) and are not supported.
- The data model supports diffing two versions (compare their `version_files` lists); the rollback / diff **UI** is deferred, v1 only guarantees history is captured in full.
