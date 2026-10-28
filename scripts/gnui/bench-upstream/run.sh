#!/bin/bash
# Zap (Go UI) against upstream ZapFast (egui), both on the demo's sample
# chats, measured by the same external harness, interleaved.
#   run.sh <rounds>
set -u
rounds=${1:-6}
here=${BENCH_DIR:-/tmp/zfbench} # bench (built from bench.swift), zap and zapfast-upstream
out=$here/results.jsonl
export ZAPFAST_CORE=${ZAPFAST_CORE:?the core-release libzapfast_core.dylib}
zap=($here/zap -demo)
up=($here/zapfast-upstream --demo)
zap_tour=($here/zap --demo-tour --demo-tour-delay 3000)
up_tour=($here/zapfast-upstream --demo-tour --demo-tour-delay 3000)

run() { # label settle idle active -- argv...
    local label=$1; shift
    printf '{"round":%s,"load":"%s",' "$round" "$(sysctl -n vm.loadavg | awk '{print $2}')" >> "$out"
    "$here/bench" "$label" "$@" 2>>"$here/apps.err" | sed 's/^{//' >> "$out"
    sleep 3
}

: > "$out"
caffeinate -d -w $$ &
# One unmeasured launch each, so every measured start is warm.
round=0
run warmup-zap 1 1 0 -- "${zap[@]}"
run warmup-upstream 1 1 0 -- "${up[@]}"
for round in $(seq 1 "$rounds"); do
    # Alternate who goes first.
    if [ $((round % 2)) = 1 ]; then order="zap upstream"; else order="upstream zap"; fi
    for who in $order; do
        if [ $who = zap ]; then a=("${zap[@]}"); t=("${zap_tour[@]}"); else a=("${up[@]}"); t=("${up_tour[@]}"); fi
        # Start, then idle 50-60 s after launch (macOS charges trimmed
        # drawables to a process for about 30 s after launch).
        run "$who" 50 10 0 -- "${a[@]}"
        # Upstream's 41-second launch tour, which each app plays by itself.
        run "$who-tour" 2.5 0 43 -- "${t[@]}"
    done
done
echo done >> "$here/run.done"
