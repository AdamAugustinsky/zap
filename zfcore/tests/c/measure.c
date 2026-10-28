/*
 * Measures the demo core through the C ABI, and checks that zfcore.h
 * compiles as C: the time from zf_core_open to the last chunk of the chat
 * list, and the process's footprint before loading, after loading, and
 * with the core open.
 *
 *   cc -std=c11 -Wall -Wextra -Werror -I zfcore/include zfcore/tests/c/measure.c -o measure
 *   ./measure target/core-release/libzapfast_core.dylib [runs]
 */
#include <dlfcn.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <mach/mach.h>

#include <stddef.h>
#include "zfcore.h"

/* zf_voice as zfcore and the Go binding lay it out. */
_Static_assert(sizeof(zf_voice) == 96 && offsetof(zf_voice, waveform) == 32, "zf_voice's layout");

#define SYM(name) __typeof__(&name) p_##name = (__typeof__(&name))dlsym(lib, #name); \
    if (!p_##name) { fprintf(stderr, "missing %s\n", #name); return 1; }

static double now_ms(void) {
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return ts.tv_sec * 1e3 + ts.tv_nsec / 1e6;
}

static double footprint_mb(void) {
    task_vm_info_data_t info;
    mach_msg_type_number_t count = TASK_VM_INFO_COUNT;
    if (task_info(mach_task_self(), TASK_VM_INFO, (task_info_t)&info, &count) != KERN_SUCCESS) return -1;
    return info.phys_footprint / 1048576.0;
}

static size_t put_u32(uint8_t *p, uint32_t v) { memcpy(p, &v, 4); return 4; }
static size_t put_u64(uint8_t *p, uint64_t v) { memcpy(p, &v, 8); return 8; }
static size_t put_str(uint8_t *p, const char *s) {
    size_t n = strlen(s);
    put_u32(p, (uint32_t)n);
    memcpy(p + 4, s, n);
    return 4 + n;
}

int main(int argc, char **argv) {
    if (argc < 2) { fprintf(stderr, "usage: measure path/to/libzapfast_core.dylib [runs]\n"); return 2; }
    int runs = argc > 2 ? atoi(argv[2]) : 5;
    double before = footprint_mb();
    double t_load = now_ms();
    void *lib = dlopen(argv[1], RTLD_NOW | RTLD_LOCAL);
    if (!lib) { fprintf(stderr, "dlopen: %s\n", dlerror()); return 1; }
    t_load = now_ms() - t_load;
    double loaded = footprint_mb();
    SYM(zf_abi_version) SYM(zf_schema_hash) SYM(zf_core_open) SYM(zf_core_start)
    SYM(zf_event_fd) SYM(zf_drain) SYM(zf_core_close)
    if (p_zf_abi_version() != ZF_ABI) { fprintf(stderr, "ABI mismatch\n"); return 1; }

    uint8_t cfg[256];
    size_t n = 0;
    n += put_u32(cfg + n, ZF_ABI);
    n += put_u64(cfg + n, p_zf_schema_hash());
    cfg[n++] = 1; /* demo */
    n += put_str(cfg + n, "/nonexistent/state");
    n += put_str(cfg + n, "/nonexistent/cache");
    n += put_str(cfg + n, "1");
    n += put_str(cfg + n, ""); /* keyring service: the demo ignores it */
    n += put_str(cfg + n, ""); /* device name: the demo ignores it */

    uint8_t *buf = malloc(ZF_DRAIN_MIN);
    zf_err err;
    double best = 1e9, worst = 0, open_peak = 0;
    for (int run = 0; run < runs; run++) {
        zf_core *core = NULL;
        double t0 = now_ms();
        if (p_zf_core_open(cfg, n, &core, &err) != ZF_OK) {
            fprintf(stderr, "open: %.*s\n", (int)err.len, err.msg);
            return 1;
        }
        int done = 0;
        struct pollfd pfd = {.fd = p_zf_event_fd(core), .events = POLLIN};
        while (!done) {
            if (poll(&pfd, 1, 5000) != 1) { fprintf(stderr, "the fd stayed quiet\n"); return 1; }
            size_t len; uint32_t count;
            while (!done && p_zf_drain(core, buf, ZF_DRAIN_MIN, &len, &count, &err) == ZF_OK) {
                for (size_t off = 0; off < len;) {
                    uint32_t rlen; memcpy(&rlen, buf + off, 4);
                    const uint8_t *rec = buf + off + 4;
                    uint16_t tag; memcpy(&tag, rec, 2);
                    /* Chats = 12: tag, reserved, batch u32, last u8 */
                    if (tag == 12 && rec[8] == 1) done = 1;
                    off += 4 + rlen;
                }
            }
        }
        double dt = now_ms() - t0;
        if (dt < best) best = dt;
        if (dt > worst) worst = dt;
        double fp = footprint_mb();
        if (fp > open_peak) open_peak = fp;
        if (p_zf_core_close(core, 5000, &err) != ZF_OK) {
            fprintf(stderr, "close: %.*s\n", (int)err.len, err.msg);
            return 1;
        }
    }
    /* A real core, opened without the startup permit: upstream's runtime and
     * threads, no disk and no network. ZF_MEASURE_REAL_DIR names an empty
     * directory for it. */
    const char *real_dir = getenv("ZF_MEASURE_REAL_DIR");
    double real_fp = 0, real_open = 0;
    if (real_dir) {
        char state[1024], cache[1024];
        snprintf(state, sizeof state, "%s/state", real_dir);
        snprintf(cache, sizeof cache, "%s/cache", real_dir);
        uint8_t rcfg[2200];
        size_t m = 0;
        m += put_u32(rcfg + m, ZF_ABI);
        m += put_u64(rcfg + m, p_zf_schema_hash());
        rcfg[m++] = 0;
        m += put_str(rcfg + m, state);
        m += put_str(rcfg + m, cache);
        m += put_str(rcfg + m, "1");
        m += put_str(rcfg + m, "ai.a3lab.zap.measure");
        m += put_str(rcfg + m, ""); /* device name: upstream's */
        zf_core *core = NULL;
        double t0 = now_ms();
        if (p_zf_core_open(rcfg, m, &core, &err) != ZF_OK) {
            fprintf(stderr, "open real: %.*s\n", (int)err.len, err.msg);
            return 1;
        }
        real_open = now_ms() - t0;
        struct timespec settle = {0, 200 * 1000 * 1000};
        nanosleep(&settle, NULL);
        real_fp = footprint_mb();
        if (p_zf_core_close(core, 10000, &err) != ZF_OK) {
            fprintf(stderr, "close real: %.*s\n", (int)err.len, err.msg);
            return 1;
        }
    }
    printf("dlopen: %.2f ms\n", t_load);
    if (real_dir) printf("real core (no permit): zf_core_open %.3f ms, phys_footprint %.1f MB\n", real_open, real_fp);
    printf("zf_core_open -> last chat-list chunk (demo), %d runs: best %.3f ms, worst %.3f ms\n", runs, best, worst);
    printf("phys_footprint: %.1f MB before dlopen, %.1f MB loaded, %.1f MB with the demo core open\n", before, loaded, open_peak);
    return 0;
}
