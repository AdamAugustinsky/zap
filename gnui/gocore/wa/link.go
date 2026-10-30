package wa

import (
	"context"
	"log/slog"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"zapfast/abi"
	"zapfast/gocore/out"
)

// Linking and the connection, as upstream's worker reports them: Link
// carries the LinkStatus whenever it changes, Unlinked with the QR code to
// show and the phone-number pairing in progress, Me once the account is
// known.

// removeTimeout is how long RemoveAccount waits for the phone to unlink
// before the account goes anyway (upstream's 15 s).
const removeTimeout = 15 * time.Second

func linkFailed(msg string) abi.LinkStatus {
	var s abi.LinkStatus
	s.Tag = abi.LinkStatusTagFailed
	s.Failed.Error = b(out.Toast(msg))
	return s
}

func linkDisconnected(reason string) abi.LinkStatus {
	var s abi.LinkStatus
	s.Tag = abi.LinkStatusTagDisconnected
	s.Disconnected.Reason = b(out.Clip(reason, 1024))
	return s
}

func linkTag(tag abi.LinkStatusTag) abi.LinkStatus { return abi.LinkStatus{Tag: tag} }

// unlinked is Unlinked with what linking has so far.
func (e *Engine) unlinked() abi.LinkStatus {
	var s abi.LinkStatus
	s.Tag = abi.LinkStatusTagUnlinked
	u := &s.Unlinked
	if e.qr != "" && len(e.qr) <= 1024 {
		u.QR, u.HasQR = b(e.qr), true
	}
	if e.pairCode != "" {
		u.PairCode, u.HasPairCode = b(out.Clip(e.pairCode, 64)), true
	}
	if e.pairingPhone != "" {
		u.PairingPhone, u.HasPairingPhone = b(out.Clip(e.pairingPhone, 64)), true
	}
	return s
}

// setStatus emits Link when the status changed.
func (e *Engine) setStatus(s abi.LinkStatus) {
	ev := abi.Event{Tag: abi.EventTagLink, Link: abi.EventLink{Status: s}}
	rec, err := ev.Encode(nil)
	if err != nil {
		return
	}
	if key := string(rec); key != e.statusKey {
		e.statusKey, e.status = key, s
		e.out.Emit(&ev)
	}
}

func (e *Engine) linkIs(tags ...abi.LinkStatusTag) bool {
	for _, t := range tags {
		if e.status.Tag == t && e.statusKey != "" {
			return true
		}
	}
	return false
}

// emitMe sends the account's identity, when it is linked.
func (e *Engine) emitMe() {
	pn, lid := e.cli.OwnID()
	if pn.IsEmpty() && lid.IsEmpty() {
		return
	}
	ev := abi.Event{Tag: abi.EventTagMe}
	me := &ev.Me
	me.ID = b(e.me())
	if !lid.IsEmpty() {
		me.LID, me.HasLID = b(lid.String()), true
	}
	if name := e.meName(); name != "" {
		me.Name, me.HasName = b(out.Clip(name, 1024)), true
	}
	if about := e.meAbout(); about != "" {
		me.About, me.HasAbout = b(out.Clip(about, 1024)), true
	}
	e.out.Emit(&ev)
}

// connect starts the connection: while unlinked with the QR channel first,
// whose codes arrive as Unlinked.
func (e *Engine) connect() {
	if e.qrCancel != nil {
		e.qrCancel()
		e.qrCancel = nil
	}
	pn, lid := e.cli.OwnID()
	if pn.IsEmpty() && lid.IsEmpty() {
		ctx, cancel := context.WithCancel(e.ctx)
		ch, err := e.cli.QRChannel(ctx)
		if err != nil {
			cancel()
			e.setStatus(linkFailed("Could not start linking: " + err.Error()))
			return
		}
		e.qrCancel = cancel
		gen := e.gen
		e.h.Go("whatsapp-qr", func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-ch:
					if !ok {
						return
					}
					e.post(func() {
						if gen == e.gen {
							e.qrItem(item)
						}
					})
				}
			}
		})
		e.setStatus(e.unlinked())
	} else {
		e.setStatus(linkTag(abi.LinkStatusTagConnecting))
	}
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		if err := cli.Connect(ctx); err != nil {
			return func() {
				if gen == e.gen {
					e.setStatus(linkFailed("Could not connect to WhatsApp: " + err.Error()))
				}
			}
		}
		return nil
	})
}

// qrItem is one item of whatsmeow's QR channel.
func (e *Engine) qrItem(item whatsmeow.QRChannelItem) {
	switch item.Event {
	case whatsmeow.QRChannelEventCode:
		e.qr = item.Code
		e.setStatus(e.unlinked())
	case whatsmeow.QRChannelSuccess.Event:
		e.qr, e.pairCode, e.pairingPhone = "", "", ""
		e.setStatus(linkTag(abi.LinkStatusTagConnecting))
	case whatsmeow.QRChannelTimeout.Event:
		e.qr = ""
		e.setStatus(linkDisconnected("The link codes expired. Reconnect to get new ones"))
	case whatsmeow.QRChannelEventPasskeyRequest, whatsmeow.QRChannelEventPasskeyResponse:
		e.emitError("Your phone asked to link with a passkey: link with the QR code or the phone number instead")
	case whatsmeow.QRChannelEventError:
		e.qr = ""
		msg := "Linking failed"
		if item.Error != nil {
			msg += ": " + item.Error.Error()
		}
		e.emitError(msg)
		e.setStatus(e.unlinked())
	default:
		// "err-client-outdated", "err-scanned-without-multidevice" and the
		// like end the channel.
		e.qr = ""
		e.emitError("Linking failed (" + item.Event + ")")
		e.setStatus(e.unlinked())
	}
}

func (e *Engine) pairWithPhone(phone string) {
	if !e.started {
		e.emitError("Not connected to WhatsApp yet")
		return
	}
	e.pairingPhone, e.pairCode = phone, ""
	e.setStatus(e.unlinked())
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		code, err := cli.PairPhone(ctx, phone)
		return func() {
			if gen != e.gen {
				return
			}
			if err != nil {
				e.pairingPhone = ""
				e.emitError("Could not link by phone number: " + err.Error())
			} else {
				e.pairCode = code
			}
			e.setStatus(e.unlinked())
		}
	})
}

// unlink logs this device out on the phone, then starts linking again.
func (e *Engine) unlink() {
	cli, gen := e.cli, e.gen
	e.job(func(ctx context.Context) func() {
		err := cli.Logout(ctx)
		if err != nil {
			// Offline or refused: the link is dropped here all the same,
			// as upstream's logout does, and the phone lists it until it
			// expires.
			slog.Warn("zapfast wa: unlinking", "err", err)
			cli.Disconnect()
			if ferr := cli.Forget(ctx); ferr != nil {
				slog.Warn("zapfast wa: forgetting the session", "err", ferr)
			}
		}
		return func() {
			if gen == e.gen {
				e.loggedOut()
			}
		}
	})
}

// loggedOut ends the session (the phone removed this device, or Unlink):
// the archive and the media and avatar caches are cleared, as upstream's
// on_logged_out does, the lists emptied, LoggedOut, then a new device that
// links again.
func (e *Engine) loggedOut() {
	if e.qrCancel != nil {
		e.qrCancel()
		e.qrCancel = nil
	}
	e.cli.Disconnect()
	e.qr, e.pairCode, e.pairingPhone = "", "", ""
	e.setSyncing(false)
	for chat, r := range e.older {
		r.timer.Stop()
		delete(e.older, chat)
	}
	clear(e.presence)
	clear(e.avatars)
	e.watch = nil
	// Reads for the unlinked account must not be applied for the next.
	e.early = earlyEvents{}
	e.forgetAccount()
	e.setStatus(linkTag(abi.LinkStatusTagLoggedOut))
	e.newClient(e.acct.Devices.NewDevice())
	if e.started {
		e.connect()
	}
}

// removeAccount unlinks (giving the phone removeTimeout) and stops: the app
// deletes the account's folders once it hears AccountRemoved.
func (e *Engine) removeAccount() {
	e.dead = true
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		ctx, cancel := context.WithTimeout(ctx, removeTimeout)
		defer cancel()
		if err := cli.Logout(ctx); err != nil {
			slog.Warn("zapfast wa: unlinking a removed account", "err", err)
		}
		cli.Disconnect()
		return func() { e.out.Emit(&abi.Event{Tag: abi.EventTagAccountRemoved}) }
	})
}

// reconnect drops and remakes the connection, or makes one.
func (e *Engine) reconnect() {
	if !e.started {
		return
	}
	if e.cli.IsConnected() {
		e.setStatus(linkTag(abi.LinkStatusTagConnecting))
		cli := e.cli
		e.job(func(context.Context) func() { cli.ResetConnection(); return nil })
		return
	}
	e.connect()
}

// resumed reconnects a connected link after the machine slept.
func (e *Engine) resumed() {
	if !e.linkIs(abi.LinkStatusTagConnected) {
		return
	}
	e.setStatus(linkTag(abi.LinkStatusTagConnecting))
	cli := e.cli
	e.job(func(context.Context) func() { cli.ResetConnection(); return nil })
}

// setOnline announces whether the app is in front: WhatsApp holds the
// phone's notifications back while a linked device is available.
func (e *Engine) setOnline(online bool) {
	e.online = online
	e.announcePresence()
}

func (e *Engine) announcePresence() {
	if !e.cli.IsConnected() {
		return
	}
	state := types.PresenceUnavailable
	if e.online {
		state = types.PresenceAvailable
	}
	cli := e.cli
	e.job(func(ctx context.Context) func() {
		if err := cli.SendPresence(ctx, state); err != nil {
			slog.Debug("zapfast wa: presence not announced", "err", err)
		}
		return nil
	})
}

// connected is whatsmeow's Connected: the link works.
func (e *Engine) connected() {
	if e.qrCancel != nil {
		e.qrCancel()
		e.qrCancel = nil
	}
	e.qr, e.pairCode, e.pairingPhone = "", "", ""
	e.setStatus(linkTag(abi.LinkStatusTagConnected))
	e.emitMe()
	e.announcePresence()
	e.retryAvatars()
	e.announcePinLimit()
	e.pumpPrefetch()
	e.mendSignalling()
}
