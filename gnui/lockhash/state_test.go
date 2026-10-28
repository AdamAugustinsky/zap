package lockhash_test

import (
	"testing"

	"zapfast/lockhash"
	"zapfast/state"
)

// state still makes the locked chats' verifier when it rewrites a legacy
// plaintext code (zf:src/settings.rs load); until it calls this package, the
// two must agree both ways.
func TestStatesLockedChatVerifierAgrees(t *testing.T) {
	t.Parallel()
	var st state.Settings
	st.SetChatLockCode(" 8642 ")
	if !lockhash.VerifiesChat(string(st.ChatLockHash), []byte("8642")) || lockhash.VerifiesChat(string(st.ChatLockHash), []byte("8643")) {
		t.Errorf("state's verifier %q", st.ChatLockHash)
	}
	st.ChatLockHash = []byte(lockhash.ChatVerifier([]byte("8642")))
	if !st.VerifyChatLockCode("8642") || st.VerifyChatLockCode("8643") {
		t.Errorf("state refuses %q", st.ChatLockHash)
	}
}
