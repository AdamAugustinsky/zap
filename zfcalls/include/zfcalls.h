/*
 * zfcalls.h: Zap's 1:1 call media, for the Go backend (gnui/gocore/calls).
 *
 * The library runs whatsapp-rust's media engine over a relay that Go's
 * signaling (on whatsmeow) found, with the callKey Go decrypted or made. It
 * holds no WhatsApp socket and no Signal state: it opens the relay, the
 * microphone, the speaker and the camera, encodes and decodes the audio and
 * the H.264 video, and keeps the decoded frames for the UI.
 *
 * Conventions:
 *  - Every function but zc_abi_version, zc_free and zc_frame_release takes
 *    a caller-owned zc_err, filled when the call fails.
 *  - Parameters, commands and events are small UTF-8 JSON documents; their
 *    shapes are zfcalls/src/wire.rs's (Open, Command, Event).
 *  - Nothing calls back into the caller: events wait in a queue that
 *    zc_next_event drains.
 *  - Every entry point catches panics (ZC_E_PANIC).
 *  - A context carries at most one call at a time, numbered by the caller's
 *    generation; a generation's last event is {"kind":"ended"}.
 */
#ifndef ZFCALLS_H
#define ZFCALLS_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#define ZC_ABI 1

#define ZC_OK 0
#define ZC_E_ARG (-1)       /* a null or invalid argument */
#define ZC_E_SMALL (-2)     /* the buffer is too small; the data waits for a larger one */
#define ZC_E_MALFORMED (-3) /* the JSON does not parse */
#define ZC_E_PANIC (-7)
#define ZC_E_NOT_FOUND (-8) /* no such call, or no frame yet */
#define ZC_E_STATE (-10)    /* a call is still open; a frame is still held */
#define ZC_E_IO (-11)
#define ZC_E_REFUSED (-13)  /* the parameters cannot make a call (no relay, a bad key) */

/* Frame formats: I420 only (zfcore's ZF_FRAME_I420). */
#define ZC_FRAME_I420 1

/* The frame streams. */
#define ZC_STREAM_LOCAL 0  /* our camera */
#define ZC_STREAM_REMOTE 1 /* the peer's */

typedef struct zc_err {
  int32_t code;
  uint32_t len;     /* bytes of msg used */
  uint8_t msg[256]; /* UTF-8, not NUL-terminated */
} zc_err;

/* A video frame, field for field zfcore's zf_frame. The planes follow each
 * other from px: Y has h rows of stride[0] bytes, U and V (h+1)/2 rows. */
typedef struct zc_frame {
  const uint8_t *px;
  uint32_t w, h;
  uint32_t stride[3];
  uint32_t fmt;
  int64_t pts_ns; /* grows with every new frame */
} zc_frame;

typedef struct zc_ctx zc_ctx;

uint32_t zc_abi_version(void);

/* One account's calls: a runtime, the frame table, the event queue. */
zc_ctx *zc_new(zc_err *err);

/* Ends the live call and frees ctx. No frame may be held, and no other
 * call on ctx may be running. */
void zc_free(zc_ctx *ctx);

/* Opens a call's media. params is Open:
 *   {"call_id", "generation", "outgoing", "self_lid", "peer_lid",
 *    "call_key" (base64, 32 bytes), "relay" (base64 of a packed node that
 *    holds a <relay>), "mlow", "video", "camera_on"?, "microphone"?,
 *    "speaker"?, "camera"?, "silent"?}
 * ZC_E_STATE while another call is open; ZC_E_REFUSED when the parameters
 * cannot make a call. */
int32_t zc_open(zc_ctx *ctx, const uint8_t *params, size_t len, zc_err *err);

/* Changes call generation's media. command is {"op": ...}: mute, rekey,
 * video_start, video_enable, camera_on, camera_off, video_stop,
 * peer_video, orientation, keyframe, microphone, speaker, camera_device.
 * ZC_E_NOT_FOUND when generation is not the open call. */
int32_t zc_command(zc_ctx *ctx, uint64_t generation, const uint8_t *command, size_t len, zc_err *err);

/* Ends call generation's media; its "ended" event follows once every
 * device is released. Nothing when it is not open. */
int32_t zc_close(zc_ctx *ctx, uint64_t generation, zc_err *err);

/* Writes the next event into buf, waiting up to timeout_ms: its length, 0
 * when none came, or a negative ZC_E_* (ZC_E_SMALL keeps the event for a
 * larger buffer). An event is {"generation", "kind", "reason"?}: connected,
 * failed, closed, silent, stalled, audible, remote_video, camera_stopped,
 * ended. One caller at a time. */
int64_t zc_next_event(zc_ctx *ctx, uint32_t timeout_ms, uint8_t *buf, size_t cap, zc_err *err);

/* The microphones, speakers and cameras as JSON ({"microphones": [{"id",
 * "label"}], "speakers": [...], "cameras": [...]}): its length, or a
 * negative ZC_E_*. It opens every device to name it: not on a UI thread. */
int64_t zc_devices(zc_ctx *ctx, uint8_t *buf, size_t cap, zc_err *err);

/* The newest frame of stream, held until zc_frame_release(stream).
 * ZC_E_NOT_FOUND while there is none; ZC_E_STATE while the last is held. */
int32_t zc_frame_acquire(zc_ctx *ctx, uint32_t stream, zc_frame *frame, zc_err *err);

void zc_frame_release(zc_ctx *ctx, uint32_t stream);

#ifdef __cplusplus
}
#endif

#endif /* ZFCALLS_H */
