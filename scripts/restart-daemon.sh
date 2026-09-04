#!/bin/sh
# Restart a running daemon so it runs the binary that was just installed.
#
#   scripts/restart-daemon.sh ~/.local/bin/helios
#
# A daemon started before the install keeps serving the old code from the old
# inode, and nothing about that is visible: the CLI is new, the API answers,
# and the fix that was just built is not in it. Restarting is the only way the
# install means anything to a machine that was already running.
#
# Sessions are not affected. Each one is a `helios ptyhost` in its own session
# (setsid, released by the parent), so it outlives the daemon that spawned it
# and is picked up again on the next start. The tunnel outlives it too.
#
# POSIX sh on purpose, and silent when no daemon is running.

set -eu

BIN="${1:?usage: restart-daemon.sh <path to the installed binary>}"

if [ -t 1 ]; then
  B=$(printf '\033[1m'); DIM=$(printf '\033[2m'); Y=$(printf '\033[33m'); N=$(printf '\033[0m')
else
  B=''; DIM=''; Y=''; N=''
fi
note() { printf '    %s%s%s\n' "$DIM" "$1" "$N"; }
warn() { printf '%s !  %s%s\n' "$Y" "$1" "$N" >&2; }

# `helios daemon status` exits 0 either way, so the answer is in what it says.
# "helios daemon is not running" does not contain "daemon is running".
"$BIN" daemon status 2> /dev/null | grep -q 'daemon is running' || exit 0

printf '%sRestarting the daemon so it runs the build just installed%s\n' "$B" "$N"
note "sessions and the tunnel stay up"

if ! "$BIN" daemon stop; then
  warn "The daemon did not stop. It is still running the older build."
  note "Restart it yourself with:  helios daemon stop && helios daemon start -d"
  exit 0
fi

if ! "$BIN" daemon start -d; then
  warn "The daemon stopped but did not come back."
  note "Start it with:  helios daemon start -d"
  exit 1
fi
