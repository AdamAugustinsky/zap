// The C side of gnui/gocore/av's WebP binding: libwebp's simple and
// animation APIs behind plain functions, so Go reaches them through
// gonativeui/clib without laying out libwebp's structs itself.
// Adapted from zapfast-go 3104c46 (internal/media/webp_native.c), MIT.

#include <stdint.h>
#include <stdlib.h>
#include "src/webp/decode.h"
#include "src/webp/demux.h"
#include "src/webp/encode.h"
#include "src/webp/mux.h"

#define ZF_EXPORT __attribute__((visibility("default")))

// The version of this shim's functions. Go refuses a library whose version
// differs.
ZF_EXPORT int32_t zf_webp_version(void) { return 1; }

// A still lossy WebP of RGBA pixels (straight alpha), or 0. *out is freed
// with zf_webp_free.
ZF_EXPORT size_t zf_webp_encode(const uint8_t *rgba, int32_t w, int32_t h, float quality, uint8_t **out) {
    *out = NULL;
    return WebPEncodeRGBA(rgba, w, h, w * 4, quality, out);
}

ZF_EXPORT void zf_webp_free(void *p) { WebPFree(p); }

typedef struct {
    WebPAnimEncoder *enc;
    WebPConfig config;
    int w, h;
} zf_anim;

// An animation encoder for frames of w x h, lossy at quality, looping
// forever, or 0.
ZF_EXPORT uintptr_t zf_webp_anim_new(int32_t w, int32_t h, float quality) {
    zf_anim *a = calloc(1, sizeof *a);
    if (a == NULL) return 0;
    WebPAnimEncoderOptions options;
    if (!WebPAnimEncoderOptionsInit(&options) || !WebPConfigInit(&a->config)) {
        free(a);
        return 0;
    }
    options.anim_params.loop_count = 0;
    options.minimize_size = 1;
    a->config.quality = quality;
    a->config.method = 4;
    a->w = w;
    a->h = h;
    a->enc = WebPAnimEncoderNew(w, h, &options);
    if (a->enc == NULL) {
        free(a);
        return 0;
    }
    return (uintptr_t)a;
}

// Adds a frame of RGBA pixels shown from ms. 1 on success.
ZF_EXPORT int32_t zf_webp_anim_add(uintptr_t h, const uint8_t *rgba, int32_t ms) {
    zf_anim *a = (zf_anim *)h;
    WebPPicture pic;
    if (!WebPPictureInit(&pic)) return 0;
    pic.use_argb = 1;
    pic.width = a->w;
    pic.height = a->h;
    if (!WebPPictureImportRGBA(&pic, rgba, a->w * 4)) {
        WebPPictureFree(&pic);
        return 0;
    }
    int ok = WebPAnimEncoderAdd(a->enc, &pic, ms, &a->config);
    WebPPictureFree(&pic);
    return ok;
}

// Ends the animation at ms and assembles it; *out is freed with
// zf_webp_free. 0 on failure.
ZF_EXPORT size_t zf_webp_anim_finish(uintptr_t h, int32_t ms, uint8_t **out) {
    zf_anim *a = (zf_anim *)h;
    *out = NULL;
    if (!WebPAnimEncoderAdd(a->enc, NULL, ms, NULL)) return 0;
    WebPData data;
    WebPDataInit(&data);
    if (!WebPAnimEncoderAssemble(a->enc, &data)) {
        WebPDataClear(&data);
        return 0;
    }
    *out = (uint8_t *)data.bytes;
    return data.size;
}

ZF_EXPORT void zf_webp_anim_delete(uintptr_t h) {
    zf_anim *a = (zf_anim *)h;
    WebPAnimEncoderDelete(a->enc);
    free(a);
}
