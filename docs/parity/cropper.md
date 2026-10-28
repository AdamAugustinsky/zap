# Parity: ui/cropper

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

Rows of `docs/PARITY.md` owned by `ui/cropper`. Evidence is in `gnui/ui/cropper` (`cropper_test.go`, `geometry_test.go`, `prepare_test.go`, `alloc_test.go`). Run: `go test ./ui/cropper` in `gnui`.

| id | status | evidence | note |
|---|---|---|---|
| CR-001 | done | TestOpensWithItsControls, TestFileIsInspectedThenOriginalIsKept | Opens from `Dialog{Kind: DialogCropper, N: attachment}`; the region is drawn over the picture. Visual look not captured in a committed file: hand: open a staged picture in the composer and check the dimmed outside and the handles. |
| CR-002 | done | TestDragTheCrop, TestDragAnEdgeMovesThatSideOnly, TestDragInsideMovesWithoutResizing, TestCropStaysInsideThePicture | Edges, corners and the inside drag correctly. Cursors as upstream: horizontal and vertical resize on the edges, diagonal (NWSE, NESW) on the corners, grab over the crop and grabbing while it moves. Not tested: the shape is a draw-time hint (`NextCursor`), not in the semantic tree. hand: hover each handle and the crop, then drag the crop; the pointer must change as described. |
| CR-003 | done | TestTurnAndReset, TestTurningCarriesTheSelection, TestEditTurnsUnderTheCropAndResets, TestRenderTurnsClockwise, TestTurnedPreviewIsMade, TestWebPPreview | Turn works for every format the system decoder reads: on macOS PNG, JPEG, GIF, WebP, HEIC, TIFF and BMP (HEIC, TIFF and BMP checked by hand); AVIF and ICO keep Turn disabled. |
| CR-004 | done | TestEnterKeepsAndEscapeLeaves | Enter or the check keeps; Escape or the close button leaves. |
| CR-005 | done | TestKeepSendsThePastedPicture, TestFileIsInspectedThenOriginalIsKept | The cropped copy is written by the core (`StagePicture`/crop command); the original is the source and is never written. Transparency is the core's. |
| CR-006 | done | TestFileIsInspectedThenOriginalIsKept | The per-account origins map reopens on the original with the chosen crop. |
| CR-007 | done | TestKeepSendsThePastedPicture | Pasted pixels are read from the staged blob on a goroutine. |
| CR-008 | done | TestUnreadablePictureToasts, TestNonPictureDoesNotOpen, TestDocumentDoesNotOpen, TestRemovedAttachmentClosesIt | Toast reads "Could not use this image: <core error>". |

Not a row: steady frames allocate nothing (TestSteadyFramesAllocateNothing); EXIF orientation uprighting for turned previews (TestRenderUprightsByExif: dimensions; the flips are the framework's `img.Orient`).

A turned or pasted preview is decoded by the framework's `img.Decode` (ImageIO on macOS, EXIF orientation applied, scaled to the preview size): TestWebPPreview, TestRotatedJPEGPreviewIsUpright (orientations 1, 3, 6, 8, pixel for pixel), TestRenderUprightsByExif. The cropper keeps no decoder of its own; `golang.org/x/image` is gone from its imports (it stays in `go.mod` as an indirect requirement of the text shaper).

Open: temp preview PNGs are removed on close, but a rapid run of turns can leak one.
