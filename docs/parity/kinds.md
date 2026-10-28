# Parity: ui/conversation/kinds

`docs/PARITY.md` holds the status of record for every row; this file holds the evidence (tests, captures, hand steps and notes).

The rows of `docs/PARITY.md` (owner `ui/conversation`) that the content blocks of `ui/conversation/kinds` draw: pictures, stickers, documents, locations, contacts, polls, interactive messages, carousels, and the notes for deleted, phone-only, unsupported and call content. Tests are in `gnui/ui/conversation/kinds` unless they are marked as transcript tests (`gnui/ui/conversation`).

Status values: **done**, **partial**, **todo**, **waived** (with the reason) and **hand** (implemented, and checked only by hand).

| id | status | evidence | note |
|---|---|---|---|
| CV-033 | done | TestSmoke, transcript TestCallEntryCallsBack | A call entry shows who called, voice or video, how long, or missed, declined or not answered. Its menu calls back (calls.Place), or in a group reads "Group calls happen on the phone". |
| MK-006 | done | TestInviteLinkOpensTheJoinDialog, transcript TestTextLinks; TestMessageCallLinksJoinThroughTheHub | Link previews and links in text both go through kinds.OpenLink. Call links go to `calls.JoinLink`, as from the system. |
| MK-008 | done | TestLinkPreviewOpensItsLink, TestGallery, transcript TestQuoteAndCardsSettle, TestDemoTranscriptShowsItsContent (demo core); capture `e-ada-links-c.png` | Cards (link previews, documents, polls, interactive cards) span the bubble's settled width (kinds.SizeIn): a preview as wide as its text, up to the column. |
| MK-009 | done | TestPhotoShowsItsFileAndOpensTheViewer, TestUndownloadedPhotoShowsItsThumbnailAndDownloadsOnClick, TestDownloadingAndFailedPhotos, TestPictureThatCannotBeDecodedOpensInTheSystem | |
| MK-011 | done | TestAnimatedStickerPlaysAndOpensTheViewer; viewer TestAStickerShowsOnACardWithAddToFavorites, TestAFavoriteStickerOffersRemoveFromFavorites, TestAStickerClosesOnAClickOnItAndEscape, TestAStickerClosesOnAClickOutsideIt; inspected `sticker-add.png`, `sticker-remove.png` (VIEWER_SHOT) | Unframed stickers open the viewer's existing animated card. SaveSticker adds to favorites, ForgetSticker removes the saved content-hash copy; card/backdrop click and Escape close. The viewer implementation was already present. Suppressing SaveSticker makes its command assertion fail. See viewer.md MK-011. |
| MK-012 | done | TestStickerPackViewsItsStickers, transcript TestDemoTranscriptShowsItsContent (demo core); capture `d-berlin-c.png` | |
| MK-013 | done | TestGIFPictureAnimatesAndOpensInTheSystem; av TestAGIFLoopsMutedUnderThePointer, TestGIFUsesFilePosterWhilePlayerLoads, TestGIFPausesWithoutDrawingAndResumesOnHover, TestGIFSteadyPlaybackAllocatesNothing, TestGIFDownloadFollowsTheAccountSetting | Picture GIFs already animate. MP4 GIFs are handled by av on media.Video, not awaiting the framework. They loop muted on focused hover, use img.VideoPoster while loading, pause hidden/offscreen, and allocate nothing steadily. See av.md for policy and captures. |
| MK-018 | done | av TestAMotionPhotoLoopsItsClipOverThePhoto, TestAMotionClipDownloadsFirst, TestAMotionClipOutOfViewAndItsStops, TestMotionFramesAllocateNothing | kinds' motion badge (`picture.go` motionBadge) in the photo's corner. A click calls `av.ToggleMotion`: av plays the clip over the photo, muted and looping, downloading it first if need be. A second click goes back to the photo. Before the badge, pictureBuild calls `av.Motion`, which draws the clip and says what the badge shows: the pause sign while it plays, a spinner while it downloads or loads, the alert after a failed download, else play. Details are in av.md MK-018. The 64 MiB limit is the core's. |
| MK-021 | done | TestDocumentOpensInItsApplicationOrDownloads, TestDocumentOfAnUnknownKindIsNotOpened, transcript TestDemoTranscriptShowsItsContent (demo core); capture `d-ada-up.png` | |
| MK-022 | done | TestLocationCardOpensAMap, transcript TestDemoTranscriptShowsItsContent (demo core); capture `d-margaret-top.png` | |
| MK-023 | done | TestLiveLocationSaysWhenItWasUpdatedAndWhenItEnded | |
| MK-024 | done | TestSharedContactStartsAChatOrIsAdded, transcript TestDemoTranscriptShowsItsContent (demo core); capture `d-margaret-top.png` | |
| MK-025 | done | TestSmoke, transcript TestDeletesShowAtOnce | |
| MK-026 | done | TestSmoke | |
| MK-027 | done | TestSmoke, TestInteractiveCardWithAnImageAndAPhoneNote | |
| PL-003 | done | TestPollShowsEveryAnswerWithItsCountAndYourChoice, transcript TestDemoTranscriptShowsItsContent (demo core); capture `d-margaret-top.png` | |
| PL-004 | done | TestClickingAnAnswerVotesAndClickingAgainTakesItBack, TestMultipleAnswersAddUpToTheLimit | |
| PL-005 | done | TestVotingIsBlockedWithAReason | |
| PL-006 | done | TestPollSaysWhenEarlierVotesAreMissingAndAsksForThem | |
| IA-001 | done | TestInteractiveCardWithAnImageAndAPhoneNote | |
| IA-002 | done | TestInteractiveButtonsSendTheirReplies | |
| IA-003 | done | TestInteractiveButtonsSendTheirReplies, TestCarouselCardsReplyForThemselves | |
| IA-005 | done | TestInteractiveButtonsSendTheirReplies | |
| IA-006 | done | TestInteractiveCardWithAnImageAndAPhoneNote; conversation `TestUnavailableCallExplainsOnHover`; inspected nested-label and padding captures on framework `693d3b38` (2026-10-23) | Phone icon and muted text retained. The actual transcript's native registration and drawn explanation pass over the nested label and padding; no reply is sent. Fail-before and framework inner-priority/removal/clipping evidence: [conversation.md](conversation.md#ia-006-integrated-nested-tooltip-proof-2026-10-23). |
| IA-007 | done | TestInteractiveRepliesNeedAConnectionAndWaitForOneInFlight | |
| IA-008 | hand | hand: answer a button from the phone | The answer shows as an ordinary reply with its quote (transcript). |
| IA-009 | hand | hand: select an interactive body, search for it, then Copy text | |
| IA-010 | done | TestShortCarouselKeepsItsCardsTogetherWithoutArrows, transcript TestBlocksAndTheirText | |
| IA-011 | done | TestCarouselScrollsWithArrowsAndShiftWheel | |
| IA-012 | done | TestCarouselCardPictureOpensTheViewerAtItsCard, TestVisiblePhotosDownloadAutomaticallyOnceWithinTheLimit | Card images open in the viewer at their card's index. |
