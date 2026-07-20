# Repo layout: one flat module for core+TUI, GUI shells are separate-module guests under `apps/`

The repo is a **single Go module** (`github.com/tiaot33/skills-tui`) laid out flat: `library/` (the facade, ADR 0004), `tui/` (Bubble Tea, ADR 0009), `cmd/skills/` (the composition root — no subcommand runs the TUI, `serve` runs RPC, ADR 0008). We rejected a two-tier `apps/` + `packages/` monorepo with a `go.mod` per package and a `go.work` workspace: that shape only earns its keep when packages version independently or carry divergent dependency trees, and a single-user local tool has neither. Multi-module would just tax every facade change with cross-module sync.

## The decisions

- **Core + TUI live in the root module; no `go.work` in v1.** The TUI is Go, in-process, and part of the core module's natural build — it is not an "app" in the `apps/` sense. The composition root is `cmd/skills`.
- **`apps/` is reserved, not scaffolded.** A future desktop GUI (Wails / Electron / AppKit — undecided, ADR 0004) lands here as its **own module** with its own `go.mod` (+ `wails.json` + `frontend/` for Wails), linked to the core via a `go.work` introduced *at that point*. Reserving costs one `apps/README.md`; scaffolding now would drag a GUI's dependency tree and build tooling into the tool speculatively. Wails' native tooling stays fully usable when deferred — it just runs from `apps/desktop-*/` when that day comes.
- **The asymmetry (TUI in `cmd/`, GUI in `apps/`) is intentional.** Go in-process frontends belong to the core module's build; heavier, separately-versioned, differently-tooled GUI shells are guests. If full symmetry is ever wanted, moving `cmd/skills` into `apps/tui/` is a cheap mechanical move — no lock-in.
- **`rpc/` and `clients/` are reserved too.** Both exist only to serve **non-Go** GUIs over the stdio sidecar (ADR 0008); the TUI never touches RPC. With the GUI deferred they have no v1 consumer, so they wait alongside `apps/`. When built, `rpc/` is the JSON-RPC server (server side) and `clients/ts` (generated) + `clients/swift` (hand-written) are the client stubs (the other end of the same contract, pinned by `rpc/testdata/`).
- **`clients/` stays separate from `apps/`.** The client stubs are generated contract artifacts and potential shared dependencies of more than one consumer; an app shell is authored product code. Keeping generated-vs-authored apart, and giving the transport client a home next to `rpc/` rather than inside any one UI, is the reason for the split.

## Consequences

- v1's real deliverable is `library/` + `tui/` + `cmd/skills/`. `apps/`, `rpc/`, `clients/` carry only a README recording the reservation and pointing at the governing ADR.
- Introducing the first GUI is additive: a new module under `apps/`, a `go.work` at the root, and (for non-Go GUIs) filling in `rpc/` + the matching `clients/` stub. No existing code moves.
