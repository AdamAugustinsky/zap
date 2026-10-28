// Ported from whatsapp-rust 5e72baf wacore/src/voip/mlow/smpl_gennoise.rs, MIT.
//
// CELP noise generator (`smpl_celp_gen_noise` + `add_noise_uv`) and its helpers (`smpl_RAND`,
// `smpl_gen_rand_pulses`, `smpl_get_env`/`get_env0`, `smpl_sigmoid`, `smpl_spec_fact2`, the noise
// DCT) plus `smpl_decode_resnrg` and `smpl_get_normalized_bitrate`.
//
// The decoder runs this per subframe: it derives the shaped residual noise that the real codec
// adds into the LPC excitation before synthesis. Without it the excitation is bare FCB pulses
// (buzzy/robotic, starved highs). The unvoiced branch is the dominant path; the voiced branch
// shapes a small high-band noise floor.
//
// The float constants are byte-exact copies of the reference f32 literals.
// SMPL_PI is the verbatim literal `3.1415926535897f` (shared.go smplPi, the same f32);
// SMPL_MAX_SF_LEN (160) and SMPL_RES_NRG_BIAS are shared.go's smplMaxSfLen / smplResNrgBias.

package mlow

import "sync"

const (
	smplNoiseCorrOrder = 2
	smplNoiseDctOrder  = 16
	smplCelpFsKhz      = 16
)

const (
	smplDecNoiseVNoiseGain  float32 = 0.35
	smplDecNoiseUvNoiseGain float32 = 0.8
	smplDecNoiseUvFcornerHz float32 = 800.0
	smplEnvSmthCoefV        float32 = 0.95
	smplEnvSmthCoefUv       float32 = 0.995
	smplEnvSmthCoefUvV      float32 = 0.99
)

// smplRand is `smpl_RAND`: LCG, wrapping i32 arithmetic (C uses u32 mul + add on the i32 seed).
func smplRand(seed int32) int32 {
	// smpl_MLA_ovflw(907633515, seed, 196314165) = 907633515 + (u32)seed * 196314165
	return int32(907633515) + int32(uint32(seed)*196314165)
}

// gennoiseSmplSigmoid is `smpl_sigmoid` with the same +-80 clamp as C (avoids inf/denormal under
// fast-math).
func gennoiseSmplSigmoid(x float32) float32 {
	if x > 80.0 {
		return 1.0
	}
	if x < -80.0 {
		return 0.0
	}
	return 1.0 / (1.0 + expF32(-x))
}

func gennoiseSmplNrg(x []float32) float32 {
	nrg := float32(0.0)
	for _, v := range x {
		nrg += float32(v * v)
	}
	return nrg
}

func smplSum(x []float32) float32 {
	s := float32(0.0)
	for _, v := range x {
		s += v
	}
	return s
}

func gennoiseSmplMaximum(x []float32) float32 {
	m := x[0]
	for _, v := range x[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// smplGenRandPulses is `smpl_gen_rand_pulses` (live version): 4-at-a-time bit-rotated white pulses
// scaled by 8.1e-10.
func smplGenRandPulses(noise []float32, l int, seed *int32) {
	const sc float32 = 8.1e-10
	i := 0
	for i+3 < l {
		*seed = smplRand(*seed)
		s := uint32(*seed)
		noise[i] = sc * float32(*seed)
		noise[i+1] = sc * float32(int32(s<<8))
		noise[i+2] = sc * float32(int32(s<<16))
		noise[i+3] = sc * float32(int32(s<<24))
		i += 4
	}
	for i < l {
		*seed = smplRand(*seed)
		noise[i] = sc * float32(*seed)
		i++
	}
}

// smplGetEnv is `smpl_get_env`: squared-signal smoothing envelope (4-wide; the accumulation order is
// load-bearing for bit-exactness).
func smplGetEnv(exc []float32, l int, smthCoef float32, smthState *float32, env []float32) {
	smthCoef = float32(smthCoef * smthCoef) // operate on squared signal
	state := *smthState + 1e-8
	state = float32(state * state)
	gainCoef := 1.0 - smthCoef
	smthCoef2 := float32(smthCoef * smthCoef)
	gainSmthCoef := float32(gainCoef * smthCoef)
	i := 0
	for i+3 < l {
		tmp0 := float32(exc[i]*exc[i]) + float32(exc[i+1]*exc[i+1])
		tmp1 := float32(exc[i+2]*exc[i+2]) + float32(exc[i+3]*exc[i+3])
		y1 := float32(gainCoef*tmp1) + float32(gainSmthCoef*tmp0) + float32(smthCoef2*state)
		y0 := float32(gainCoef*tmp0) + float32(smthCoef*state)
		env[i] = sqrtF32(y0)
		env[i+1] = env[i]
		env[i+2] = sqrtF32(y1)
		env[i+3] = env[i+2]
		state = y1
		i += 4
	}
	*smthState = env[l-1]
}

// smplGetEnv0 is `smpl_get_env0`: decaying envelope when there is no excitation to seed from.
func smplGetEnv0(l int, smthCoef float32, smthState *float32, env []float32) {
	smthCoef2 := float32(smthCoef * smthCoef)
	env[0] = float32((*smthState + 1e-8) * smthCoef)
	env[1] = env[0]
	i := 2
	for i+2 < l {
		env[i+2] = float32(env[i-1] * smthCoef2)
		env[i+3] = env[i+2]
		env[i] = float32(env[i-1] * smthCoef)
		env[i+1] = env[i]
		i += 4
	}
	env[l-2] = float32(env[l-3] * smthCoef)
	env[l-1] = env[l-2]
	*smthState = env[l-1]
}

// Filters.

// gennoiseSmplFiltMa1 is `smpl_filt_ma1` (coef_len=2, state_len=1). `x != y`.
func gennoiseSmplFiltMa1(x []float32, n int, coef *[2]float32, state *[1]float32, y []float32) {
	if coef[0] == 1.0 {
		for k := 1; k < n; k++ {
			y[k] = x[k] + float32(coef[1]*x[k-1])
		}
	} else {
		for k := 0; k < n; k++ {
			y[k] = float32(coef[0] * x[k])
		}
		for k := 1; k < n; k++ {
			y[k] += float32(coef[1] * x[k-1])
		}
	}
	y[0] = float32(coef[0]*x[0]) + float32(coef[1]*state[0])
	state[0] = x[n-1]
}

// gennoiseSmplFiltAr1 is `smpl_filt_ar1` (coef_len=2, state_len=1, coef[0]==1).
func gennoiseSmplFiltAr1(x []float32, n int, coef *[2]float32, state *[1]float32, y []float32) {
	ar1 := -coef[1]
	ytmp := state[0]
	for nn := 0; nn < n; nn++ {
		ytmp = x[nn] + float32(ytmp*ar1)
		y[nn] = ytmp
	}
	state[0] = ytmp
}

// smplFiltArma1 is `smpl_filt_arma1`: MA1 then AR1, state {ma, ar}. Supports in-place (x==y) via a
// temp buffer.
func smplFiltArma1(x []float32, n int, coefMa *[2]float32, coefAr *[2]float32, state *[2]float32, y []float32) {
	var tmp [smplMaxSfLen]float32
	maState := [1]float32{state[0]}
	gennoiseSmplFiltMa1(x, n, coefMa, &maState, tmp[:])
	state[0] = maState[0]
	arState := [1]float32{state[1]}
	gennoiseSmplFiltAr1(tmp[:], n, coefAr, &arState, y)
	state[1] = arState[0]
}

// gennoiseSmplFiltMa2 is `smpl_filt_ma2` (coef_len=3, state_len=2). `x != y`.
func gennoiseSmplFiltMa2(x []float32, n int, coef *[3]float32, state *[2]float32, y []float32) {
	if coef[0] == 1.0 {
		for i := 1; i < n; i++ {
			y[i] = x[i] + float32(coef[1]*x[i-1])
		}
	} else {
		for i := 0; i < n; i++ {
			y[i] = float32(coef[0] * x[i])
		}
		for i := 1; i < n; i++ {
			y[i] += float32(coef[1] * x[i-1])
		}
	}
	for i := 2; i < n; i++ {
		y[i] += float32(coef[2] * x[i-2])
	}
	y[0] = float32(coef[0]*x[0]) + float32(coef[1]*state[0]) + float32(coef[2]*state[1])
	y[1] += float32(coef[2] * state[0])
	state[0] = x[n-1]
	state[1] = x[n-2]
}

// smplSpecFact2 is `smpl_spec_fact2`: spectral factorization of a 3-tap autocorrelation into a
// 3-tap MA.
func smplSpecFact2(cIn *[3]float32, a *[3]float32) {
	c := *cIn
	c[0] += 1e-30
	invC0 := 1.0 / c[0]
	r2 := float32(c[2] * invC0)
	r1 := c[1] / float32(c[0]*(1.0+r2))
	for range 2 {
		v0 := 1.0 + float32(r1*r1) + float32(r2*r2)
		v1 := r1 + float32(r1*r2)
		s := -2.0 / v0
		da0 := float32(s * r1)
		da1 := float32(s * r2)
		s = float32(v0 * invC0)
		e1 := float32(s*c[1]) - v1
		e2 := float32(s*c[2]) - r2
		r0 := float32(2.0*r1) + float32(v0*da0)
		r3 := float32(2.0*r2) + float32(v0*da1)
		rr00 := float32(r0 * r0)
		rr01 := float32(r0 * r3)
		rr11 := float32(r3 * r3)
		rcap1 := 1.0 + r2 + float32(v1*da0)
		r4 := r1 + float32(v1*da1)
		rr00 += float32(rcap1 * rcap1)
		rr01 += float32(rcap1 * r4)
		rr11 += float32(r4 * r4)
		re0 := float32(rcap1 * e1)
		re1 := float32(r4 * e1)
		r2c := float32(r2 * da0)
		r5 := 1.0 + float32(r2*da1)
		rr00 += float32(r2c * r2c)
		rr01 += float32(r2c * r5)
		rr11 += float32(r5 * r5)
		re0 += float32(r2c * e2)
		re1 += float32(r5 * e2)
		s = float32(rr00*rr11) - float32(rr01*rr01)
		if s < 1e-4 {
			break
		}
		s = 1.0 / s
		r1 += float32((float32(rr11*re0) - float32(rr01*re1)) * s)
		r2 += float32((float32(-rr01*re0) + float32(rr00*re1)) * s)
	}
	sc := sqrtF32(c[0] / (1.0 + float32(r1*r1) + float32(r2*r2)))
	a[0] = sc
	a[1] = float32(sc * r1)
	a[2] = float32(sc * r2)
}

// noiseDctOnce backs noiseDct (the Rust OnceLock).
var noiseDctOnce = sync.OnceValue(func() *[smplNoiseCorrOrder + 1][smplNoiseDctOrder]float32 {
	var m [smplNoiseCorrOrder + 1][smplNoiseDctOrder]float32
	sc := float32(1.0) / sqrtF32(float32(smplNoiseDctOrder))
	for i := 0; i < smplNoiseDctOrder; i++ {
		// The /16 compiles to an exact *0.0625; the conversion keeps it from fusing into omega +=.
		dOmega := float32(((0.5 + float32(i)) * smplPi) / float32(smplNoiseDctOrder))
		omega := float32(0.0)
		for j := 0; j < smplNoiseCorrOrder+1; j++ {
			m[j][i] = float32(cosF32(omega) * sc)
			omega += dOmega
		}
	}
	return &m
})

// noiseDct is the noise DCT (`dct_mat_t[NOISE_CORR_ORDER+1][NOISE_DCT_ORDER]`).
func noiseDct() *[smplNoiseCorrOrder + 1][smplNoiseDctOrder]float32 {
	return noiseDctOnce()
}

// gennoiseMatrixMultTransp16 is `smpl_matrix_mult_transp_16`: y[0..16] = sum_j C[j*16 + i] * x[j].
func gennoiseMatrixMultTransp16(c *[smplNoiseCorrOrder + 1][smplNoiseDctOrder]float32, x []float32, y []float32, lenX int) {
	var yt [smplNoiseDctOrder]float32
	xtmp := x[0]
	for i := 0; i < smplNoiseDctOrder; i++ {
		yt[i] = float32(c[0][i] * xtmp)
	}
	for j := 1; j < lenX; j++ {
		xtmp := x[j]
		for i := 0; i < smplNoiseDctOrder; i++ {
			yt[i] += float32(c[j][i] * xtmp)
		}
	}
	copy(y[:smplNoiseDctOrder], yt[:])
}

// matrixMult is `smpl_matrix_mult`: y[i] = dot(C[i*len_x ..], x, len_x). C here is dct_mat_t
// flattened by [j][i].
func matrixMult(c *[smplNoiseCorrOrder + 1][smplNoiseDctOrder]float32, x []float32, y []float32) {
	// C is treated row-major as C[i][k], but dct_mat_t is [k][i]. The call reads dct_mat_t flat with
	// len_y=CORR+1, len_x=DCT_ORDER, so row i is the flat offset i*DCT_ORDER, i.e. dct_mat_t[i].
	for i := 0; i < smplNoiseCorrOrder+1; i++ {
		acc := float32(0.0)
		for k := 0; k < smplNoiseDctOrder; k++ {
			acc += float32(c[i][k] * x[k])
		}
		y[i] = acc
	}
}

// smplGetNormalizedBitrate is `smpl_get_normalized_bitrate` (`smpl_helpers.c`).
func smplGetNormalizedBitrate(numPulses int32, frameLength16 int32) float32 {
	pulsesPer20ms := float32(numPulses*frameLength16) / (20.0 * 16.0)
	return gennoiseSmplSigmoid(float32(1.4*log2F32(pulsesPer20ms+1.0)) - 6.5)
}

// smplDecodeResnrg is `smpl_decode_resnrg` (`smpl_quant_nrg_res.c`). `nrgres_frame_dbq_Q14` is the
// per-subframe Q14 residual-energy value; returns the linear residual energy scaled by the subframe
// length.
func smplDecodeResnrg(nrgresFrameDbqQ14 int32, fcbSubfrlen int32) float32 {
	exp := 0.1 * (float32(nrgresFrameDbqQ14) / float32(int32(1)<<14))
	resnrg := powF32(10.0, exp) - smplResNrgBias
	if resnrg < 0.0 {
		resnrg = 0.0
	}
	return resnrg * float32(fcbSubfrlen)
}

// noiseGenerator is the persistent decoder-side noise generator state (mirrors C
// `NoiseGenerator`). A plain value: Go assignment is Rust's derived Clone.
type noiseGenerator struct {
	envSmth       float32
	envLast       float32
	outStateUv    [2]float32
	outStateV     [2]float32
	corrSmth      [smplNoiseCorrOrder + 1]float32
	shapeState    [smplNoiseCorrOrder]float32
	prevVoiced    bool
	sinceUnvoiced int32
	randSeed      int32
}

// defaultNoiseGenerator: the whole struct is zero-initialized at decode init.
func defaultNoiseGenerator() noiseGenerator {
	return noiseGenerator{}
}

var coefMaV = [3]float32{0.25, -0.496, 0.25}

// addNoiseUv is `add_noise_uv`: HP-shape the unvoiced noise (corner climbs when LSF[0..1] are
// close) and add it into `noise`. `fcbgains_uv` is the codec's UV gain table (only `lsf[0..1]` are
// read).
func addNoiseUv(ng *noiseGenerator, excNoiseUv []float32, l int, lsf []float32, nrgRatio float32, noise []float32) {
	lsfHz := 16000.0 * (lsf[0] + lsf[1]) / (4.0 * smplPi)
	minUvFcornerHz := lsfHz * 3.0 * gennoiseSmplSigmoid(0.2/(lsf[1]-lsf[0]+1e-30)-3.0)
	uvFcornerHz := smplDecNoiseUvFcornerHz * minF32(float32(0.6)+float32(0.4*nrgRatio), 1.0)
	uvFcornerHz = maxF32(uvFcornerHz, minUvFcornerHz)
	uvFcornerHz = minF32(uvFcornerHz, 1500.0)
	coefTmp := 6.0 * uvFcornerHz / 16000.0
	coefMaUv := [2]float32{
		float32((1.0 - float32(0.5*coefTmp)) * smplDecNoiseUvNoiseGain),
		-float32((1.0 - float32(0.5*coefTmp)) * smplDecNoiseUvNoiseGain),
	}
	coefArUv := [2]float32{1.0, -1.0 + coefTmp}
	var filtered [smplMaxSfLen]float32
	smplFiltArma1(excNoiseUv, l, &coefMaUv, &coefArUv, &ng.outStateUv, filtered[:])
	copy(excNoiseUv[:l], filtered[:l])
	for i := 0; i < l; i++ {
		noise[i] += excNoiseUv[i]
	}
}

// smplCelpGenNoise is `smpl_celp_gen_noise`: build the shaped residual noise for one subframe
// (faithful port).
func smplCelpGenNoise(
	ng *noiseGenerator,
	excLpc []float32,
	l int,
	voiced bool,
	numPulses int32,
	nrgres float32,
	fcbgIdx int32,
	lsf []float32,
	normalizedBitrate float32,
	fcbgainsUv []float32,
	noise []float32,
) {
	nrgRatio := float32(1.0)
	var noiseUv [smplMaxSfLen]float32
	var noiseV [smplMaxSfLen]float32
	var noiseV2 [smplMaxSfLen]float32
	var env [smplMaxSfLen]float32

	if voiced {
		var corrs [smplNoiseCorrOrder + 1]float32
		var c [smplNoiseCorrOrder + 1]float32
		var ctgt [smplNoiseCorrOrder + 1]float32
		for i := 0; i < smplNoiseCorrOrder+1; i++ {
			acc := float32(0.0)
			for k := 0; k < l-i; k++ {
				acc += float32(excLpc[k] * excLpc[k+i])
			}
			corrs[i] = acc
		}
		corrs[0] += 1e-12
		var corrSmthCoef float32
		if l == smplCelpFsKhz*10 {
			corrSmthCoef = 0.4
		} else {
			corrSmthCoef = 0.16
		}
		for i := 0; i < smplNoiseCorrOrder+1; i++ {
			ng.corrSmth[i] += float32(corrSmthCoef * (corrs[i] - ng.corrSmth[i]))
		}
		scale := smplDecNoiseVNoiseGain * smplDecNoiseVNoiseGain * corrs[0] / ng.corrSmth[0]
		for i := 0; i < smplNoiseCorrOrder+1; i++ {
			c[i] = float32(ng.corrSmth[i] * scale)
		}
		c[1] *= 2.0
		c[2] *= 2.0

		dct := noiseDct()
		var f2 [smplNoiseDctOrder]float32
		var f2Tgt [smplNoiseDctOrder]float32
		gennoiseMatrixMultTransp16(dct, c[:], f2[:], smplNoiseCorrOrder+1)
		m := float32(gennoiseSmplMaximum(f2[:smplNoiseDctOrder]) * 1.5)
		for i := 0; i < smplNoiseDctOrder; i++ {
			f2Tgt[i] = m - f2[i]
		}
		matrixMult(dct, f2Tgt[:], ctgt[:])
		smplGenRandPulses(noiseV[:], l, &ng.randSeed)
		if !ng.prevVoiced {
			ng.envSmth = ng.envLast
		}
		smplGetEnv(excLpc, l, smplEnvSmthCoefV, &ng.envSmth, env[:])
		for i := 0; i < l; i++ {
			noiseV[i] = float32(noiseV[i] * env[i])
		}
		nrgNoise := gennoiseSmplNrg(noiseV[:l])
		inv := 1.0 / (nrgNoise + 1e-12)
		for i := 0; i < smplNoiseCorrOrder+1; i++ {
			ctgt[i] = float32(ctgt[i] * inv)
		}
		var coefMa [smplNoiseCorrOrder + 1]float32
		smplSpecFact2(&ctgt, &coefMa)
		gennoiseSmplFiltMa2(noiseV[:], l, &coefMa, &ng.shapeState, noiseV2[:])

		if !ng.prevVoiced {
			smplGenRandPulses(noiseUv[:], l, &ng.randSeed)
			envVal := float32(ng.envLast * smplEnvSmthCoefUvV)
			i := 0
			for i < l {
				noiseUv[i] = float32(noiseUv[i] * envVal)
				noiseUv[i+1] = float32(noiseUv[i+1] * float32(envVal*smplEnvSmthCoefUvV))
				envVal = float32(envVal * (smplEnvSmthCoefUvV * smplEnvSmthCoefUvV))
				i += 2
			}
		} else if ng.sinceUnvoiced < 2 {
			for i := 0; i < l; i++ {
				noiseUv[i] = 0.0
			}
		}
		ng.envLast = env[l-1]
	} else {
		for i := range ng.corrSmth {
			ng.corrSmth[i] = 0.0
		}
		for i := range ng.shapeState {
			ng.shapeState[i] = 0.0
		}
		for i := 0; i < l; i++ {
			noiseV2[i] = 0.0
		}

		var nrgTgt float32
		if numPulses > 0 {
			nrgRatio = gennoiseSmplNrg(excLpc[:l]) / (nrgres + 1e-20)
			hardness := 10.0 + float32(20.0*normalizedBitrate)
			nrgTgt = nrgres * lnF32(expF32(float32(hardness*(1.0-nrgRatio)))+1.0) / hardness
			smplGetEnv(excLpc, l, smplEnvSmthCoefUv, &ng.envSmth, env[:])
		} else {
			nrgRatio = 0.0
			nrgTgt = nrgres
			smplGetEnv0(l, smplEnvSmthCoefUv, &ng.envSmth, env[:])
		}

		scale := 1.0 / float32(l)
		nrgTgt = float32(nrgTgt*scale) + 1e-30
		nrgEnv := float32(gennoiseSmplNrg(env[:l]) * scale)
		f := sqrtF32(nrgTgt)
		g := sqrtF32(nrgTgt / nrgEnv)
		ge := float32(g * env[0])
		envLast := ng.envLast
		if envLast < minF32(f, ge) {
			if f < ge {
				g = 0.0
			} else {
				f = 0.0
			}
		} else if envLast > maxF32(f, ge) {
			if f > ge {
				g = 0.0
			} else {
				f = 0.0
			}
		} else {
			sumEnv := float32(smplSum(env[:l]) * scale)
			a := nrgEnv + float32(env[0]*env[0]) - float32(2.0*sumEnv*env[0])
			b := float32(2.0 * envLast * (sumEnv - env[0]))
			cc := float32(envLast*envLast) - nrgTgt
			tmp := float32(b*b) - float32(4.0*a*cc)
			if tmp < 1e-35 || a < 1e-25 {
				f = 0.0
				g = 0.0
			} else {
				tmp = sqrtF32(tmp)
				scale = 0.5 / a
				g = float32((-b + tmp) * scale)
				f = envLast - float32(env[0]*g)
				if f < 0.0 {
					g = float32((-b - tmp) * scale)
					f = envLast - float32(env[0]*g)
				}
			}
		}

		smplGenRandPulses(noiseUv[:], l, &ng.randSeed)
		if numPulses > 0 {
			maxVal := float32(fcbgainsUv[int(fcbgIdx)] * 0.5)
			for i := 0; i < l; i++ {
				if excLpc[i] == 0.0 {
					noiseUv[i] = float32(noiseUv[i] * minF32(f+float32(g*env[i]), maxVal))
				} else {
					noiseUv[i] = 0.0
				}
			}
			ng.envLast = minF32(f+float32(g*env[l-1]), maxVal)
		} else {
			for i := 0; i < l; i++ {
				noiseUv[i] = float32(noiseUv[i] * (f + float32(g*env[i])))
			}
			ng.envLast = f + float32(g*env[l-1])
		}
	}

	if ng.prevVoiced || voiced {
		gennoiseSmplFiltMa2(noiseV2[:], l, &coefMaV, &ng.outStateV, noise)
	} else {
		for i := 0; i < l; i++ {
			noise[i] = 0.0
		}
	}
	if ng.sinceUnvoiced < 2 || !voiced {
		addNoiseUv(ng, noiseUv[:], l, lsf, nrgRatio, noise)
	} else {
		ng.outStateUv = [2]float32{0.0, 0.0}
	}
	ng.prevVoiced = voiced
	if voiced {
		ng.sinceUnvoiced++
	} else {
		ng.sinceUnvoiced = 0
	}
}
