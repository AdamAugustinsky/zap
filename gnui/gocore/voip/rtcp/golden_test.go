package rtcp

import (
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// Reports built by whatsapp-rust 5e72baf's rtcp.rs, including NTP times
// whose seconds wrap 32 bits.
func TestGoldenReports(t *testing.T) {
	g := kat.Golden(t).Sub(t, "rtcp")
	var entropy [12]byte
	copy(entropy[:], g.Hex(t, "entropy"))
	cname := WhatsAppCName(entropy)
	ssrc, remote := uint32(g.Uint(t, "ssrc")), uint32(g.Uint(t, "remote_ssrc"))
	stats := SenderStats{
		PacketsSent: uint32(g.Uint(t, "stats", "packets_sent")), OctetsSent: uint32(g.Uint(t, "stats", "octets_sent")),
		RTPTimestamp: uint32(g.Uint(t, "stats", "rtp_timestamp")),
	}
	c208, c209, sdes, empty := Compact208(ssrc, remote), Compact209(ssrc), SourceDescription(ssrc, cname), EmptySourceDescription(ssrc)
	for name, got := range map[string][]byte{"cname": cname[:], "compact208": c208[:], "compact209": c209[:], "sdes": sdes[:], "empty_sdes": empty[:]} {
		if hex.EncodeToString(got) != g.Str(t, name) {
			t.Errorf("%s: %x", name, got)
		}
	}
	for _, r := range g.List(t, "reports") {
		now := uint64(r.Get(t, "now_ms").(float64))
		sr := SenderReport(ssrc, stats, now)
		if hex.EncodeToString(sr[:]) != r.Str(t, "sender_report") {
			t.Errorf("SR at %d: %x", now, sr)
		}
		if got := hex.EncodeToString(SenderReportWithSDES(ssrc, stats, now, cname)); got != r.Str(t, "sender_report_with_sdes") {
			t.Errorf("SR+SDES at %d", now)
		}
	}
}
