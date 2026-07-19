# Chinese-first UI, i18n deferred

The v1 TUI ships Chinese-only. Full internationalisation (i18n) is a later expansion, not part of the first version.

To keep that expansion cheap, user-facing strings are centralised in one place (a strings table) rather than scattered inline, so adding locales later is additive. We stop short of building the full locale-selection machinery now, since only Chinese exists in v1 and there is nothing to switch between.

Domain terms keep an English canonical name (used in code and the glossary) alongside the Chinese term shown in the UI — see `CONTEXT.md`.
