#!/usr/bin/env bash
# Explores random seeds of gocore's deterministic simulation
# (gnui/gocore/wa/sim_test.go) for a while, printing each seed before it
# runs, and stops at the first failure with the command that replays it.
# The log goes to target/sim/. Synthetic data only: no account, no network.
#
#   scripts/sim-explore.sh [duration] [mode] [steps]
#
# duration is Go's (30m, 8h; default 1h), mode default, queued, ingest,
# lostack or ack (gnui/gocore/wa/sim_test.go's -sim.mode; default
# "default"), steps per seed (default 300). It needs SQLCipher
# (packaging/macos/sqlcipher/build.sh) and, as go.mod says, go-native-ui
# beside the repository or a go.work.
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
duration=${1:-1h}
mode=${2:-default}
steps=${3:-300}
mkdir -p "$root/target/sim"
log="$root/target/sim/explore-$(date +%Y%m%d-%H%M%S)-$mode.log"
echo "sim: exploring $mode for $duration, $steps steps a seed; log $log"

cd "$root/gnui"
CGO_ENABLED=0 go test ./gocore/wa -run 'TestSimExplore$' -v -count=1 -timeout 0 \
	-sim.for="$duration" -sim.mode="$mode" -sim.steps="$steps" 2>&1 |
	grep -v '^=== RUN\|^    --- PASS\|^--- PASS' | tee "$log"
