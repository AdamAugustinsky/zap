package core

import (
	"time"

	"zapfast/abi"
)

// The DemoIncoming texts that ring a call on the demo core instead of
// filing a message: a voice or a video call from the sender, in the chat; a
// group chat rings a group call.
const (
	DemoCallText      = "zf-demo:call"
	DemoVideoCallText = "zf-demo:video-call"
)

// DemoCall sets cmd to ring a call from caller in chat on the demo core
// after delay. The demo misses it after 30 s unless it is answered.
func DemoCall(cmd *abi.Command, chat, caller string, video bool, delay time.Duration) {
	text := DemoCallText
	if video {
		text = DemoVideoCallText
	}
	*cmd = abi.Command{Tag: abi.CommandTagDemoIncoming}
	cmd.DemoIncoming = abi.CommandDemoIncoming{
		DelayMS: uint32(min(delay.Milliseconds(), 1<<32-1)),
		Chat:    []byte(chat),
		Sender:  []byte(caller),
		Text:    []byte(text),
	}
}
