// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_nrgres.rs, MIT.

package mlow

import "testing"

// The reconstruction must match the reference dump: for the first internal frame the committed
// nrgres_frame_qi=0, nrgres_shape_qi=8 yield these exact per-subframe nrgres_dbq_Q14 (which the
// decoder reads back as gain_q).
func TestSmplNrgres_DbqReconstructionMatchesCDump(t *testing.T) {
	got := dbqFromIndices(0, 8)
	want := [4]int32{-1390064, -1392336, -1394000, -1394176}
	if got != want {
		t.Fatalf("dbq_from_indices(0, 8): got %v want %v", got, want)
	}
}

// Round-trip: quantizing a residual energy and reconstructing from the resulting indices must agree
// with the direct per-subframe dbq the quantizer reports.
func TestSmplNrgres_QuantThenReconstructConsistent(t *testing.T) {
	nrgres := [4]float32{0.01, 0.02, 0.005, 0.03}
	q := quantNrgRes4(&nrgres)
	if want := dbqFromIndices(q.frameQi, int(q.shapeQi)); q.dbqQ14 != want {
		t.Fatalf("dbq_q14: got %v want %v", q.dbqQ14, want)
	}
}
