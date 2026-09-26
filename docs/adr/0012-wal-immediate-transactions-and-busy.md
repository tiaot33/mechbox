# Multi-process coexistence floor: WAL, immediate transactions, and a retryable `busy`

ADR 0008 accepted that two processes may open the same SQLite file at once and deferred the real fix — one owning daemon — to topology B. This ADR sets the floor under topology A: two processes on one file must never block each other's reads, must never lose a finished write at commit time, and must report a genuine write collision as something the caller can retry rather than as a bug.

Under a rollback journal, readers and writers exclude each other: a long read in one process (walking a large skill's files) made the other process's write fail *at commit* after the driver's 5 s busy timeout, with all the transaction's work already done; a long write did the same to every write in the other process. Both surfaced as `internal("database is locked")`.

## The decisions

- **The Library switches its file to `journal_mode=WAL`.** `Open` runs the pragma only after identity is confirmed — after an existing library's application id and schema version check out, or after a new library has its tables, `application_id` and `user_version` written — because the journal mode lives in the file header and running it earlier would mutate a foreign database that `Open` is about to reject. If the pragma does not yield `wal` (a filesystem without shared memory, an older binary still holding a connection), `Open` continues in the current mode: that is exactly the status quo, and the other two decisions still hold. The next `Open` tries again.
- **Every transaction begins with `BEGIN IMMEDIATE`** (driver DSN `_txlock=immediate`), with `busy_timeout` written out explicitly as 5000 ms. All Library transactions are write transactions — pure reads run in autocommit — so IMMEDIATE costs nothing, lets writers queue through the busy handler instead of failing on upgrade, and rules out `SQLITE_BUSY_SNAPSHOT` from a deferred read-then-write under WAL. Maintainers no longer audit each transaction for "does it read before writing".
- **`SQLITE_BUSY` / `SQLITE_LOCKED` map to a new `Busy` error code**, matched on the primary result code so `BUSY_SNAPSHOT`, `BUSY_RECOVERY` and friends are covered. It means "another process held the write lock past the wait limit; retry later". The Library only reports; whether and how often to retry is the caller's call. It is distinct from `SourceUnreachable` so a local lock is never presented as a network fault.
- **Sync images stay on a rollback journal.** The backup API copies the WAL marker in the header along with the pages, so the push snapshot is switched back to `journal_mode=DELETE` before it is read; older versions can still open a pushed image. Pulling replaces the local file through the backup API into a WAL destination, which keeps it WAL — one pull does not silently revert the mode.
- **One connection per process is kept** (`SetMaxOpenConns(1)`). In-process serialization still comes from it; "no `l.db` calls inside a transaction" in `activation.go` is unchanged. WAL changes what happens *between* processes, not within one.

## Considered and rejected

- **Daemon now (topology B).** Solves this and more — change notifications, atomic compound disk projections across processes — but adds daemon lifecycle and moves in-process callers off the facade. Rejected as before (ADR 0008); this ADR is the floor that makes A tolerable until B arrives.
- **Set WAL through the DSN.** Simplest, but it takes effect before identity is checked and would rewrite the header of any `.db` the user points at by mistake, breaking `Open`'s "reject a foreign database without touching it" guarantee.
- **Automatic retry inside the Library on `Busy`.** Hides latency the caller should show and makes cancellation semantics murkier; a stable code plus caller-side retry is smaller and honest.
- **A configurable `busy_timeout`.** No evidence anyone needs a different number; the value is written out so it is visible, not so it is tunable.

## Consequences

- Two `Library` instances on one file interleave reads and writes without errors; a write in one no longer waits for a read in the other, and vice versa. Only two concurrent *writers* contend, and the loser gets `Busy` after 5 s.
- **Disk layout changes.** The library directory gains `library.db-wal` and `library.db-shm`. SQLite checkpoints and removes `-wal` when the last connection closes cleanly; `-shm` may remain and is harmless. **Manual backups must not copy `library.db` alone while a process has it open** — pending writes live in `-wal`. Use the tool's Sync (its image is a consistent snapshot), or copy the file when no process has it open.
- Legacy libraries are switched on their first `Open` by this version; schema version and contents are unchanged, so a synced image still opens on an older version.
- Listing queries read in a single statement (one snapshot), so a skill removed by the other process mid-list no longer fails the whole list.
- **Not solved here** (topology B): atomicity of compound disk-projection operations across processes, and change notification between processes — a view in one process still goes stale until it reloads.
- This ADR settles the multi-process coexistence floor; the effective journal mode should become visible through a status call when one exists.
