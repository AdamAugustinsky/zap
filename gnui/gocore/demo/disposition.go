package demo

import "zapfast/abi"

// Disposition is what the demo core does with a command, decided before
// the command takes any blob (zfcore's demo::Disposition).
type Disposition uint8

const (
	// Refuses: refused with ZF_E_STATE, "the demo core does not answer X".
	Refuses Disposition = iota
	// Answers: it answers with events, as the worker would.
	Answers
	// Accepts: taken, and nothing comes back, as with the real core.
	Accepts
)

func (d Disposition) String() string {
	switch d {
	case Answers:
		return "Answers"
	case Accepts:
		return "Accepts"
	}
	return "Refuses"
}

// answers and accepts are zfcore's table (demo.rs, disposition); every
// other command is refused. A test checks them against demo.rs.
var (
	answers = []abi.CommandTag{
		abi.CommandTagLoadChat,
		abi.CommandTagSendText,
		abi.CommandTagSendLocation,
		abi.CommandTagSendImage,
		abi.CommandTagSendVoice,
		abi.CommandTagMarkRead,
		abi.CommandTagSetMuted,
		abi.CommandTagOpenLog,
		abi.CommandTagReconnect,
		abi.CommandTagWatchReceipts,
		abi.CommandTagFetchAvatar,
		abi.CommandTagListChatShared,
		abi.CommandTagStartCall,
		abi.CommandTagJoinCallLink,
		abi.CommandTagAnswerCall,
		abi.CommandTagDeclineCall,
		abi.CommandTagHangupCall,
		abi.CommandTagSetCallMuted,
		abi.CommandTagSetCallCamera,
		abi.CommandTagSetCallMicrophone,
		abi.CommandTagSetCallSpeaker,
		abi.CommandTagSetCallCameraDevice,
		abi.CommandTagRefreshCallDevices,
		abi.CommandTagLoadCallLog,
		abi.CommandTagLoadStorageStats,
		abi.CommandTagClearMediaCache,
		abi.CommandTagFetchAccountPrivacy,
		abi.CommandTagSetAccountPrivacy,
		abi.CommandTagEnsureChat,
		abi.CommandTagSetLocked,
		abi.CommandTagVotePoll,
		abi.CommandTagLoadChatMedia,
		abi.CommandTagSearchGifs,
		abi.CommandTagSendGif,
		abi.CommandTagRecentStickers,
		abi.CommandTagSendSticker,
		abi.CommandTagSaveSticker,
		abi.CommandTagRemoveRecentSticker,
		abi.CommandTagForgetSticker,
		abi.CommandTagSendStickerPack,
		abi.CommandTagViewStickerPack,
		abi.CommandTagAddStickerPack,
		abi.CommandTagDemoIncoming,
		abi.CommandTagDemoTyping,
		abi.CommandTagDemoPanic,
	}
	accepts = []abi.CommandTag{
		abi.CommandTagComposing,
		abi.CommandTagSaveDraft,
		abi.CommandTagSetOnline,
		abi.CommandTagResumed,
		abi.CommandTagSetHistoryPrefetch,
		abi.CommandTagCancelCall,
		// Upstream sends the played receipt and files nothing.
		abi.CommandTagMarkPlayed,
		abi.CommandTagPickAnswer,
	}
)

var dispositions = func() map[abi.CommandTag]Disposition {
	m := make(map[abi.CommandTag]Disposition, len(answers)+len(accepts))
	for _, t := range answers {
		m[t] = Answers
	}
	for _, t := range accepts {
		m[t] = Accepts
	}
	return m
}()

// DispositionOf is what the demo core does with a command of tag.
func DispositionOf(tag abi.CommandTag) Disposition { return dispositions[tag] }
