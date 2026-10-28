package wa

import (
	"zapfast/abi"
	"zapfast/gocore"
)

// more handles WP4b's commands: labels, stars, pins, shared media, groups,
// contacts, the account, polls, stickers, GIFs, storage, the pickers and
// the settings. The calls never reach it (answers refuses them).
func (e *Engine) more(cmd *gocore.Command) {
	switch cmd.Tag {
	// Labels.
	case abi.CommandTagCreateLabel:
		e.createLabel(string(cmd.CreateLabel.Name), string(cmd.CreateLabel.ColorHex))
	case abi.CommandTagUpdateLabel:
		c := &cmd.UpdateLabel
		e.updateLabel(string(c.ID), string(c.Name), string(c.ColorHex))
	case abi.CommandTagDeleteLabel:
		e.deleteLabel(string(cmd.DeleteLabel.ID))
	case abi.CommandTagSetChatLabels:
		e.setChatLabels(string(cmd.SetChatLabels.Chat), strs(cmd.SetChatLabels.Labels))

	// Stars and pins.
	case abi.CommandTagSetStar:
		c := &cmd.SetStar
		e.setStar(string(c.Chat), string(c.Message), c.Starred)
	case abi.CommandTagLoadStarred:
		e.loadStarred()
	case abi.CommandTagSetMessagePinned:
		c := &cmd.SetMessagePinned
		e.setMessagePinned(string(c.Chat), string(c.Message), c.Pinned)
	case abi.CommandTagLoadPins:
		e.emitPins(string(cmd.LoadPins.Chat))

	// Shared media.
	case abi.CommandTagListChatShared:
		e.listChatShared(string(cmd.ListChatShared.Chat), cmd.ListChatShared.Request)
	case abi.CommandTagLoadChatMedia:
		c := &cmd.LoadChatMedia
		e.loadChatMedia(string(c.Chat), string(c.Around), c.HasAround)
	case abi.CommandTagDownloadMotion:
		e.downloadMotion(string(cmd.DownloadMotion.Chat), string(cmd.DownloadMotion.Message))

	// Groups.
	case abi.CommandTagLeaveGroup:
		e.leaveGroup(string(cmd.LeaveGroup.Chat), cmd.LeaveGroup.Archive)
	case abi.CommandTagSetGroupName:
		e.setGroupName(string(cmd.SetGroupName.Chat), string(cmd.SetGroupName.Name))
	case abi.CommandTagSetGroupPicture:
		var jpeg []byte
		if cmd.SetGroupPicture.HasJpeg {
			jpeg = cmd.Blob
		}
		e.setGroupPicture(string(cmd.SetGroupPicture.Chat), jpeg)
	case abi.CommandTagPickGroupPicture:
		e.pickGroupPicture(string(cmd.PickGroupPicture.Chat))
	case abi.CommandTagPreviewInvite:
		e.previewInvite(string(cmd.PreviewInvite.Code))
	case abi.CommandTagJoinInvite:
		e.joinInvite(string(cmd.JoinInvite.Code))

	// Contacts.
	case abi.CommandTagSaveContact:
		c := &cmd.SaveContact
		first := ""
		if c.HasFirstName {
			first = string(c.FirstName)
		}
		e.saveContact(string(c.ID), string(c.FullName), first, c.ToPhone)
	case abi.CommandTagNewContact:
		e.newContact(&cmd.NewContact)

	// The account.
	case abi.CommandTagFetchAccountPrivacy:
		e.fetchAccountPrivacy()
	case abi.CommandTagSetAccountPrivacy:
		e.setAccountPrivacy(cmd.SetAccountPrivacy.Kind, cmd.SetAccountPrivacy.Choice)
	case abi.CommandTagSetProfile:
		c := &cmd.SetProfile
		e.setProfile(string(c.Name), c.HasName, string(c.About), c.HasAbout)
	case abi.CommandTagPickProfilePicture:
		e.pickProfilePicture()

	// Polls and interactive messages.
	case abi.CommandTagCreatePoll:
		e.createPoll(string(cmd.CreatePoll.Chat), cmd.CreatePoll.Draft)
	case abi.CommandTagVotePoll:
		c := &cmd.VotePoll
		e.votePoll(string(c.Chat), string(c.Message), c.Choices)
	case abi.CommandTagRefreshPoll:
		e.refreshPoll(string(cmd.RefreshPoll.Chat), string(cmd.RefreshPoll.Message))
	case abi.CommandTagReplyInteractive:
		e.replyInteractive(&cmd.ReplyInteractive)

	// Stickers and GIFs.
	case abi.CommandTagSendSticker:
		e.sendSticker(&cmd.SendSticker)
	case abi.CommandTagSaveSticker:
		e.saveFavorite(string(cmd.SaveSticker.Path))
	case abi.CommandTagRemoveRecentSticker:
		e.removeRecentSticker(string(cmd.RemoveRecentSticker.Path))
	case abi.CommandTagForgetSticker:
		e.forgetSticker(string(cmd.ForgetSticker.Path))
	case abi.CommandTagRecentStickers:
		e.emitStickers()
	case abi.CommandTagImportStickerUrl:
		e.importStickerURL(string(cmd.ImportStickerUrl.URL))
	case abi.CommandTagPickStickerArchive:
		e.pickStickerArchive()
	case abi.CommandTagCreateStickerPack:
		e.createStickerPack(string(cmd.CreateStickerPack.Name))
	case abi.CommandTagSetStickerPack:
		c := &cmd.SetStickerPack
		e.setStickerPack(string(c.Pack), string(c.Sticker), c.Member)
	case abi.CommandTagAddStickerPack:
		e.addStickerPack(string(cmd.AddStickerPack.Dir), string(cmd.AddStickerPack.Name))
	case abi.CommandTagDeleteStickerPack:
		e.deleteStickerPack(string(cmd.DeleteStickerPack.Dir))
	case abi.CommandTagViewStickerPack:
		e.viewStickerPack(string(cmd.ViewStickerPack.Chat), string(cmd.ViewStickerPack.Message))
	case abi.CommandTagSendStickerPack:
		e.sendStickerPack(string(cmd.SendStickerPack.Chat), string(cmd.SendStickerPack.Dir))
	case abi.CommandTagMakeSticker:
		e.makeSticker(&cmd.MakeSticker)
	case abi.CommandTagPickStickerPicture:
		e.pickStickerPicture()
	case abi.CommandTagInspectPicture:
		e.inspectPicture(cmd.InspectPicture.Target, string(cmd.InspectPicture.Path))
	case abi.CommandTagApplyPictureEdit:
		e.applyPictureEdit(&cmd.ApplyPictureEdit, cmd.Blob)
	case abi.CommandTagSendGif:
		e.sendGif(&cmd.SendGif)
	case abi.CommandTagSearchGifs:
		e.searchGifs(string(cmd.SearchGifs.Query), string(cmd.SearchGifs.Key))

	// Storage.
	case abi.CommandTagLoadStorageStats:
		e.measureStorage(cmd.LoadStorageStats.Token, false)
	case abi.CommandTagClearMediaCache:
		e.measureStorage(cmd.ClearMediaCache.Token, true)
	case abi.CommandTagSetKeepChatsArchived:
		e.setKeepChatsArchived(cmd.SetKeepChatsArchived.Keep)
	case abi.CommandTagSetHistoryPrefetch:
		c := &cmd.SetHistoryPrefetch
		focused := ""
		if c.HasFocused {
			focused = string(c.Focused)
		}
		e.setHistoryPrefetch(c.On, focused)
	case abi.CommandTagSetDownloadFolder:
		e.setDownloadFolder(string(cmd.SetDownloadFolder.Path), cmd.SetDownloadFolder.HasPath)

	// Pickers and files.
	case abi.CommandTagPickFiles:
		e.pickFiles(string(cmd.PickFiles.Chat), cmd.PickFiles.Documents)
	case abi.CommandTagSaveAttachmentAs:
		e.saveAttachmentAs(string(cmd.SaveAttachmentAs.Source), string(cmd.SaveAttachmentAs.Name))
	case abi.CommandTagSaveAttachments:
		e.saveAttachments(cmd.SaveAttachments.Files)
	case abi.CommandTagPickNotificationSound:
		e.pickNotificationSound(cmd.PickNotificationSound.Mention)
	case abi.CommandTagSetChatSound:
		c := &cmd.SetChatSound
		e.setChatSound(string(c.Chat), c.Sound, c.HasSound)
	case abi.CommandTagPickChatSound:
		e.pickChatSound(string(cmd.PickChatSound.Chat))
	case abi.CommandTagPickDownloadFolder:
		e.pickDownloadFolder()
	case abi.CommandTagPickWallpaperImage:
		e.pickWallpaperImage()
	case abi.CommandTagRemoveWallpaperImage:
		e.removeWallpaperImage()
	case abi.CommandTagPrepareClipboardImage:
		e.prepareClipboardImage(string(cmd.PrepareClipboardImage.Path))
	case abi.CommandTagOpenLog:
		e.openLog(cmd.OpenLog.Path)

	// Settings.
	case abi.CommandTagSetLocked:
		e.setLocked(string(cmd.SetLocked.Chat), cmd.SetLocked.Locked)
	case abi.CommandTagSetProxy:
		e.setProxy(string(cmd.SetProxy.Proxy))
	case abi.CommandTagStartOverArchive:
		// The archive opened: there is nothing to start over from.
	}
}
