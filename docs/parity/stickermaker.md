# Parity: ui/stickermaker

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Rows of `docs/PARITY.md` owned by `ui/stickermaker`. Evidence is in `gnui/ui/stickermaker` (`stickermaker_test.go`, `square_test.go`, `alloc_test.go`). Run: `go test ./ui/stickermaker` in `gnui`.

| id | status | evidence | note |
|---|---|---|---|
| SM-001 | done | TestOpeningPicksAPicture | Opening sends `PickStickerPicture`; the core's panel (PNG, JPEG, WebP, GIF) and its `StickerPicture` event reopen the maker. |
| SM-002 | done | TestSizeAndDrag, TestSquareStaysSquareAndInsideThePicture, TestSmallestSide | Square geometry is pure Go; the preview is the picture with the square marked, as upstream draws it. Visual look: hand: open the maker on a wide picture, drag the square and move Size. |
| SM-003 | done | TestTransparentBackground | The switch appears only for a transparent picture; off sends white. |
| SM-004 | done | TestCleanEmojis, TestSendGoesToTheOpenChat | One emoji per grapheme (`text.EmojiOnly`); words are dropped. |
| SM-005 | done | TestSendGoesToTheOpenChat, TestAddToFavorites, TestSendQuotesWhatTheComposerQuotes, TestCancelMakesNothing | "Added to favorites" is the state's toast on the core's answer. |
| SM-006 | waived | n/a | The 512 × 512 WebP under 100 KB is the core's `MakeSticker`; the UI only sends the square, emojis and background choice. Covered by the core's tests. |
