package conversation

import (
	"testing"

	"gonativeui/ui"
	"gonativeui/widget/widgettest"

	"zapfast/abi"
)

// PR-002: upstream conversation.rs:5964-5986, :6007-6014 and
// :7086-7120 keep unavailable actions visible but disabled.
func TestFailedMessageCannotReloadHistory(t *testing.T) {
	r := newRig(t)
	m := msg(ada, "failed", base0, true, "not sent")
	m.Status = abi.DeliveryFailed
	r.open(ada, m)
	r.Play(widgettest.RClick(ui.RoleGroup, bubble("You", base0)), widgettest.Idle(2))
	if r.node(ui.RoleButton, "Reload earlier messages").State&ui.SemDisabled == 0 {
		t.Fatal("failed message offers Reload earlier messages")
	}
	r.Play(widgettest.Click(ui.RoleButton, "Reload earlier messages"), widgettest.Idle(2))
	if len(r.sent(abi.CommandTagReloadHistory)) != 0 {
		t.Fatal("disabled Reload sent a command")
	}
}

func TestDownloadingMenuDoesNotRequestAnotherDownload(t *testing.T) {
	for _, downloading := range []bool{false, true} {
		t.Run(map[bool]string{false: "idle", true: "downloading"}[downloading], func(t *testing.T) {
			r := newRig(t)
			m := msg(ada, "photo", base0, false, "")
			m.Content = abi.Content{Tag: abi.ContentTagImage}
			m.Content.Image.Media = abi.Media{Mime: []byte("image/jpeg"), Size: 80 << 20}
			label := "Download"
			if downloading {
				m.Content.Image.Media.State.Tag = abi.MediaStateTagDownloading
				label = "Downloading…"
			}
			r.open(ada, m)
			r.Play(widgettest.RClick(ui.RoleGroup, bubble("Ada Lovelace", base0)), widgettest.Idle(2))
			if disabled := r.node(ui.RoleButton, label).State&ui.SemDisabled != 0; disabled != downloading {
				t.Fatalf("%q disabled = %v", label, disabled)
			}
			before := len(r.sent(abi.CommandTagDownload))
			r.Play(widgettest.Click(ui.RoleButton, label), widgettest.Idle(2))
			sent := r.sent(abi.CommandTagDownload)[before:]
			if downloading {
				if len(sent) != 0 {
					t.Fatal("downloading again requested a download")
				}
			} else if len(sent) != 1 || !sent[0].Download.Manual || string(sent[0].Download.Message) != "photo" {
				t.Fatalf("manual download: %+v", sent)
			}
		})
	}
}

func TestDownloadAllWaitsOnlyWhileEveryMissingItemDownloads(t *testing.T) {
	for _, all := range []bool{false, true} {
		t.Run(map[bool]string{false: "mixed", true: "all downloading"}[all], func(t *testing.T) {
			r := newRig(t)
			var msgs []abi.Message
			for i, id := range []string{"first", "second"} {
				m := msg(ada, id, base0+int64(i), false, "")
				m.Content = abi.Content{Tag: abi.ContentTagImage}
				md := &m.Content.Image.Media
				*md = abi.Media{Mime: []byte("image/jpeg"), Size: 80 << 20, Album: []byte("album"), HasAlbum: true}
				if all || i == 0 {
					md.State.Tag = abi.MediaStateTagDownloading
				}
				msgs = append(msgs, m)
			}
			r.open(ada, msgs...)
			label := "Download all"
			if all {
				label = "Downloading…"
			}
			if disabled := r.node(ui.RoleButton, label).State&ui.SemDisabled != 0; disabled != all {
				t.Fatalf("%q disabled = %v", label, disabled)
			}
			before := len(r.sent(abi.CommandTagDownload))
			r.Play(widgettest.Click(ui.RoleButton, label), widgettest.Idle(2))
			sent := r.sent(abi.CommandTagDownload)[before:]
			if all {
				if len(sent) != 0 {
					t.Fatal("an all-downloading album sent another request")
				}
			} else if len(sent) != 1 || !sent[0].Download.Manual || string(sent[0].Download.Message) != "second" {
				t.Fatalf("only the idle member should download: %+v", sent)
			}
		})
	}
}
