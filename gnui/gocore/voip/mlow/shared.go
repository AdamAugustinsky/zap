// Constants several whatsapp-rust 5e72baf wacore/src/voip/mlow files each
// define privately with the same value (MIT). One Go package cannot repeat
// them, so they live here once. The integer ones are untyped so they serve
// both the files that declare them usize and the ones that declare them i32.

package mlow

import "math"

const (
	frameLen             = 320 // smpl_harm_postfilter, smpl_harmcomb
	maxpitchLen          = 320 // smpl_harm_postfilter, smpl_pitch_enc
	opusFrameSamps       = 960 // encode, decoder: 60 ms @ 16 kHz
	pitchNumBlocks       = 9   // smpl_pitch_enc, smpl_pitch_seed
	silkInt16Max         = 32767
	smplAcbgM            = 2
	smplCelpIdxFec       = 0
	smplCelpIdxMain      = 1
	smplCelpMaxRates     = smplCelpIdxMain + 1
	smplFcbgVN           = 34
	smplLagSubfrlen      = 40
	smplLpcOrder         = 16
	smplLtpInterpolDelay = 8
	smplMaxLResp         = 32 + 1
	smplMaxPitchLag      = 320
	smplMaxPulsesPerSf   = 40
	smplMaxSfLen         = 16 * 10
	smplPercRespLen      = 16 * 2
	smplSubfrLen         = 80 // 5 ms @ 16 kHz
	smplUvGainIdxLen     = 90
	smplWinnextWbLen     = 16 * 2
	smplWin3ShortLen     = smplWinnextWbLen
	smplWin3LongLen      = 64
)

const (
	// smplPi is 3.1415926535897 / 3.1415927410125 / f32::consts::PI in the
	// various Rust files: all the same f32.
	smplPi                  float32 = math.Pi
	smplPitchSharpeningCoef float32 = 0.9881
	smplResNrgBias          float32 = 3.1622776e-9
	smplUvGainMinDb         float32 = -90.0
	smplUvGainStepDb        float32 = 1.0
	smplVGainMinDb          float32 = -100.0
	smplVGainStepDb         float32 = 3.0
)

// smplInterpolKernel is the LTP fractional-lag interpolation kernel
// (smpl_celp and smpl_celpdec).
var smplInterpolKernel = [2 * smplLtpInterpolDelay]float32{
	-6.3925986e-6, 0.00011064114, -0.0009153038, 0.00484772, -0.018698348, 0.05759091, -0.15997477, 0.6170455,
	0.61704546, -0.15997475, 0.057590906, -0.018698348, 0.00484772, -0.0009153038, 0.000110641144, -6.392598e-6,
}
