// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_nrgres.rs, MIT.
//
// MLow unvoiced residual-energy quantizer. The unvoiced excitation LEVEL is carried entirely by the
// per-subframe quantized residual-energy floor (`nrgres_dbq_Q14`): a frame-mean scalar quant plus a
// shape VQ. Our decoder reads this floor back as `gain_q` (validated bit-exact in
// `param_decode_match`), so the encoder must produce the same `nrgres_dbq_Q14` for the round-trip
// level to be right.
//
// The reconstruction is validated against the reference dump (the `nrgres_dbq_Q14` test). It is
// wired live in `analysis.rs`'s unvoiced path: the wire gain block IS the nrgres layout
// (`gain_main`==`nrgres_frame_qi`, `gain_delta`==`nrgres_shape_qi`, the gain table == the shape
// codebook, `cb1` == the frame step), so `gain_q[sf]` decodes back as `nrgres_dbq_Q14`.

package mlow

// SMPL_RES_NRG_BIAS (3.1622776e-9) is shared.go's smplResNrgBias.
const (
	smplResNrgMinDb float32 = -85.0
	smplResNrgMaxDb float32 = 0.0
	// smplNrgStepDbQ144 is `smpl_nrg_step_db_Q14[2]` (the 4-subframe table index).
	smplNrgStepDbQ144   int32 = 16686
	smplResNrgShapeCbN4       = 98
)

// nrgresShapeCb4Q10 is `nrgres_shape_CB_4_Q10` (98 vectors x 4 subframes), stored verbatim.
var nrgresShapeCb4Q10 = [smplResNrgShapeCbN4 * 4]int16{
	-2515, -2238, 2632, 2121, 790, 3973, -2872, -1891, -533, 2847, 1453, -3767, -6174, -402, 2668, 3908,
	-1623, -1458, 153, 2928, -1254, 3197, -476, -1467, 1803, -1086, 270, -987, 1952, -66, -1257, -629,
	161, 19, -85, -96, 4833, 3147, -105, -7875, -1320, 1377, -1156, 1099, 3398, -2247, 1485, -2637,
	-3031, 2756, 1841, -1566, -1487, 2202, -2668, 1954, 5518, -5344, 522, -696, 8400, -3123, -6235, 958,
	5152, -2444, -2811, 102, 2513, -82, 1181, -3612, -561, -197, -1074, 1832, -294, -1250, -1839, 3383,
	5126, 522, -782, -4866, -7760, -5178, -1840, 14779, -1119, 6007, -1489, -3399, -4567, -2543, 1855, 5255,
	53, -1626, 67, 1506, -12256, -7706, -1982, 21943, 3549, -969, -1096, -1484, -10824, 2981, 2204, 5639,
	-229, 1106, 945, -1821, -9237, 10157, 1616, -2537, 4916, -199, -2177, -2540, 6673, 984, -3355, -4302,
	-7130, -4677, 8925, 2882, 445, 2762, -348, -2859, -196, -1859, 1761, 294, 2725, -2093, -966, 334,
	-3908, -308, 3675, 541, 735, 890, -2516, 891, 504, 1631, -1157, -977, -17817, 2119, 7104, 8594,
	-2056, 1897, -198, 356, 292, -4544, -287, 4538, -1455, -304, 603, 1156, -18259, -12643, 15247, 15655,
	4177, 1778, -1815, -4140, 1425, 576, -294, -1707, -1301, 5132, 2838, -6669, -4727, -3148, -905, 8781,
	-650, 152, -4654, 5152, 13746, 2320, -6259, -9807, -1356, 396, 3789, -2829, 2337, 1947, -29, -4256,
	6033, 820, -5730, -1123, -1795, 1091, 1080, -377, 2208, -1921, -3314, 3027, 9688, 5218, -3754, -11152,
	3814, -3941, -6183, 6310, -1017, -2391, 4393, -984, 10944, -1182, -5011, -4751, -4640, 7201, -218, -2343,
	-1278, 4720, -4212, 770, 2777, 1333, -5944, 1833, -16066, 8107, 5165, 2795, 2530, -5020, 6073, -3582,
	-2111, -7534, 4575, 5070, -8702, -3762, 4050, 8414, 1335, -997, -1567, 1229, 9348, 1534, -3959, -6922,
	2440, 1153, -2175, -1418, -2715, -4538, -4478, 11730, 569, -885, 2032, -1716, 3529, -91, -3218, -219,
	2157, -4121, 191, 1772, -2123, -1968, -1355, 5446, 1475, -354, 3651, -4772, 1654, -3521, 2726, -859,
	2393, 6820, -2958, -6255, -3861, 1365, 1177, 1319, 7614, -1638, -2789, -3187, -3628, -2635, 6902, -639,
	1925, 2295, -1451, -2769, -3683, 4517, -981, 147, -1260, -529, 2339, -550, 3013, 639, -1050, -2602,
	3651, 1959, -3218, -2391, 6267, 3124, -2926, -6464, -8180, 3900, 4191, 89, -3372, -611, 1042, 2941,
	-2510, 856, -925, 2579, -11667, -8436, 10605, 9498, 6427, -2733, 1887, -5581, 1581, -1722, -328, 469,
	2011, 1989, -3606, -394, -1014, 2197, -1200, 17, 1544, -2555, 765, 247, 1188, -183, 1966, -2972,
	-6057, 3480, -2284, 4860, -25659, 8466, 8891, 8303,
}

// nrgResQuant is the result of `smpl_quant_nrg_res` for the 4-subframe path.
type nrgResQuant struct {
	frameQi int32
	shapeQi int32
	// Per-subframe quantized residual-energy floor (Q14); the decoder reads this as `gain_q`.
	dbqQ14 [4]int32
}

// quantNrgRes4 is the residual-energy quantizer for num_subfr == 4. `nrgres[sf]` is the
// per-subframe residual energy `nrg(reslpc_sf)/subfrlen` in the SAME (int16-scaled) domain `reslpc`
// lives in.
func quantNrgRes4(nrgres *[4]float32) nrgResQuant {
	var nrgresDb [4]float32
	frameDb := float32(0.0)
	for i := 0; i < 4; i++ {
		nrgresDb[i] = minF32(10.0*log10F32(nrgres[i]+smplResNrgBias), smplResNrgMaxDb)
		frameDb += nrgresDb[i]
	}
	// Explicit conversion: Go rewrites /4 as *0.25 and would otherwise fuse it into the next
	// subtraction (exact either way, but kept FMA-free like the rest of the port).
	frameDb = float32(frameDb / 4.0)
	scQ14 := float32(1.0) / float32(int32(1)<<14)
	frameQi := f32ToI32(roundF32((frameDb - smplResNrgMinDb) / (scQ14 * float32(smplNrgStepDbQ144))))
	frameDbqQ14 := frameQi * smplNrgStepDbQ144
	frameDbqQ14 += f32ToI32(smplResNrgMinDb) * (1 << 14)
	for i := 0; i < 4; i++ {
		nrgresDb[i] -= float32(float32(frameDbqQ14) * scQ14)
	}
	// Shape VQ (min RD) over the 98 codebook vectors.
	scQ10 := float32(1.0) / float32(int32(1)<<10)
	bestRd := float32(1e30)
	qi := 0
	for n := 0; n < smplResNrgShapeCbN4; n++ {
		rd := float32(0.0)
		for i := 0; i < 4; i++ {
			d := nrgresDb[i] - float32(float32(nrgresShapeCb4Q10[n*4+i])*scQ10)
			rd += float32(d * d)
		}
		if rd < bestRd {
			qi = n
			bestRd = rd
		}
	}
	var dbqQ14 [4]int32
	for i := 0; i < 4; i++ {
		dbqQ14[i] = frameDbqQ14 + int32(nrgresShapeCb4Q10[qi*4+i])*16
	}
	return nrgResQuant{
		frameQi: frameQi,
		shapeQi: int32(qi),
		dbqQ14:  dbqQ14,
	}
}

// dbqFromIndices reconstructs the per-subframe `nrgres_dbq_Q14` from a frame/shape index pair,
// matching the unvoiced decode reconstruction. Used to validate the quantizer against the reference
// dump.
func dbqFromIndices(frameQi int32, shapeQi int) [4]int32 {
	frameDbq := frameQi*smplNrgStepDbQ144 + f32ToI32(smplResNrgMinDb)*(1<<14)
	var out [4]int32
	for sf := range out {
		out[sf] = frameDbq + int32(nrgresShapeCb4Q10[shapeQi*4+sf])*16
	}
	return out
}
