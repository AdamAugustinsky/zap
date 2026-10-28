package warp

import (
	"encoding/hex"
	"testing"

	"zapfast/gocore/voip/internal/kat"
)

// Full 20-byte MI digests from whatsapp-rust 5e72baf's compute_warp_mi_tag.
func TestGoldenMITags(t *testing.T) {
	for _, v := range kat.Golden(t).List(t, "warp_tags") {
		tag := MITag(v.Hex(t, "auth_key"), v.Hex(t, "packet"), uint32(v.Uint(t, "roc")))
		if hex.EncodeToString(tag[:]) != v.Str(t, "tag") {
			t.Fatalf("%x", tag)
		}
	}
}
