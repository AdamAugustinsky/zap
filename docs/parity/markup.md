# Parity: markup

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

The rows of `docs/PARITY.md` whose Go owner is `markup`, checked against upstream on 2026-10-21 (gnui `z2-foundation`, pass 2).

| id | status | evidence | note |
|---|---|---|---|
| MK-001 | done | markup TestInlineMarkersPairUp, TestStrayMarkersStayLiteral, TestTripleBackticksMarkMonospaceOnALine | |
| MK-002 | done | markup TestBigIsOneToThreeEmojiAlone; ui/conversation bubble.go (the jumbo font) | |
| MK-031 | done | TestAMetaAIReplyWithCodeAndATable; ui/conversation TestCaptureMetaAI; inspected capture `/private/var/folders/2p/gmsgpspx4xlg9gjphgh84c540000gn/T/opencode/pz-verify/meta-ai.png` | Upstream src/backend/worker/bot_replies.rs:59-79 flattens tables to pipe-separated text; src/demo.rs:1100-1139 uses that text, and src/markup.rs:246-290 renders it as ordinary lines, not a grid. The test checks that exact demo reply, monospace code and intact table lines; the synthetic transcript capture was visually inspected. |
