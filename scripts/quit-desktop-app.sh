#!/bin/sh
# Quit a running Helios desktop app so the next launch picks up the build that
# was just installed.
#
#   scripts/quit-desktop-app.sh [APP_DIR, macOS only]
#
# Overwriting the app's files on disk does not touch a process that already
# has the old code loaded: Electron's single-instance lock just refocuses that
# same stale window, so a build installed over a running app shows the old one
# until it is quit and reopened by hand. This does that quitting for you.
#
# POSIX sh on purpose, and silent when nothing is running.

set -eu

APP_DIR="${1:-/Applications}"

if [ -t 1 ]; then
  DIM=$(printf '\033[2m'); N=$(printf '\033[0m')
else
  DIM=''; N=''
fi
note() { printf '    %s%s%s\n' "$DIM" "$1" "$N"; }

case "$(uname -s)" in
  Darwin) PATTERN="$APP_DIR/Helios.app/Contents/MacOS/Helios" ;;
  *) PATTERN="/opt/Helios/helios-desktop" ;;
esac

pkill -f "$PATTERN" 2> /dev/null || exit 0
note "Quit the running Helios app — reopen it to see this build"
