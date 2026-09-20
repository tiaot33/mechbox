# Chinese-first, i18n deferred

All user-facing text is Chinese. Full internationalisation (i18n) is a later expansion, not part of the first version: only Chinese exists, so there is nothing to switch between and no locale-selection machinery is built now.

Domain terms keep an English canonical name — used in code and in the glossary — alongside the Chinese term shown to users; see `CONTEXT.md`. That split is what keeps the deferral cheap: identifiers never need translating, only the presentation layer does.

How the presentation layer organises its strings is a decision for the code that owns them, once such code exists.
