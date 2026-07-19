# SQLite is the Library

The Library — the tool's single source of truth — is a single SQLite database. It holds both skill **contents** (as blobs) and all **metadata**: collections, groups, activation state, and registered projects. Agent directories are projections of it, not inputs.

We chose one opaque store over skill folders on disk because it keeps the source of truth in exactly one place, gives transactional integrity across an operation that touches many skills at once, and makes S3 Sync a clean "export the database, push it" step. Sync is non-realtime, so SQLite's coarse, whole-blob nature costs nothing: a periodic export from SQLite is the sync artifact.

## Consequences

- **Install** writes a fetched skill's files into SQLite as blobs.
- **Activation** expands a skill out of SQLite into the target agent directory; **deactivation** removes that directory.
- **Self-developed skills** are edited through the tool rather than by editing files in place — content lives in the database, so authoring needs an explicit export/edit/re-import path or an in-tool editor (to be specified).
- **Sync** exports the database and pushes it to S3 (and pulls to restore), reproducing a full setup on another machine.
