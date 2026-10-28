# Parity: mentions

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

The rows of `docs/PARITY.md` whose Go owner is `mentions`, checked against upstream on 2026-10-21 (gnui `z2-foundation`, pass 2).

| id | status | evidence | note |
|---|---|---|---|
| MK-003 | done | markup TestMentionsBecomeNames, TestEveryoneIsHighlightedOnlyWithItsMention | |
| MM-009 | done | mentions TestStoredMentions, TestTheWireTokenIsAWholeWord; ui/composer TestEditingAnEveryoneMentionKeepsItsToken; Rust backend::worker::tests::text_edits_notify_everyone_only_when_adding_the_mention | The specific Rust test ran successfully on 2026-10-22 using the existing SSD headless artifact. It tests adding versus retaining @all, the outbound notify bit, and accepted edits retaining visible mention metadata; the relevant test and implementation match this worktree. Go separately verifies EditText preserves the token. See [shared-core evidence boundary](logic.md#shared-core-evidence-boundary-cv-031-mm-009-mk-033): this is not a live phone-notification claim or a fresh dylib rebuild. |
| CP-007 | done | mentions TestALocalizedAliasBecomesTheWireToken, TestThePickerQueryMatchesEitherWord, TestAliasesComeFromTheCatalog; ui/composer TestMentionEveryone | |
