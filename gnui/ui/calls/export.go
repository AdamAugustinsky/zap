package calls

import "zapfast/state"

// Place places a call to chat (a JID) of account a, a voice call or a video
// call: the call screen shows it as placing at once (the conversation
// header's call buttons). With a call up already it says to hang up first.
func Place(s *state.State, a *state.Account, chat []byte, video bool) {
	hubOf(s).place(a, chat, video)
}

// JoinLink joins a call link of account a (the token of
// call.whatsapp.com/voice/… or /video/…, a video call's for the latter): the
// call screen shows it as connecting at once, under the link's name (sys's
// JoinCall hook, MK-007). With a call up already it says to hang up first.
func JoinLink(s *state.State, a *state.Account, token []byte, video bool) {
	hubOf(s).joinLink(a, token, video)
}

// LiveHere reports whether chat (a JID) of account a has the live call: the
// conversation header shows the way back to it instead of the call buttons.
func LiveHere(s *state.State, a *state.Account, chat []byte) bool {
	return hubOf(s).liveHere(a, chat)
}
