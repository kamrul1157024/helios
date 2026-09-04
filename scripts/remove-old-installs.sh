#!/bin/sh
# Delete helios binaries left behind by earlier installs, so the one just
# installed is the only one there is.
#
#   scripts/remove-old-installs.sh ~/.local/bin/helios
#
# Installs used to land in /usr/local/bin. Leaving that copy where it is means
# the shell keeps running it — /usr/local/bin comes before ~/.local/bin on most
# PATHs — and the bug reports that follow are about a version nobody is working
# on. Prepending the new directory would only hide it, so the file goes.
#
# Two things it will not do. It will not delete anything it cannot confirm is a
# helios binary, because "helios" on someone's PATH may well be their own
# script. And it will not delete a copy under a package manager's control,
# which is that manager's to remove.
#
# POSIX sh on purpose, and silent when there is nothing to remove.

set -eu

KEEP="${1:?usage: remove-old-installs.sh <path to the installed binary>}"

if [ -t 1 ]; then
  DIM=$(printf '\033[2m'); Y=$(printf '\033[33m'); N=$(printf '\033[0m')
else
  DIM=''; Y=''; N=''
fi
note() { printf '    %s%s%s\n' "$DIM" "$1" "$N"; }
warn() { printf '%s !  %s%s\n' "$Y" "$1" "$N" >&2; }

# Where to look: every directory on the PATH, plus the place installs used to
# go. That last one earns its place by not needing to be on the PATH today —
# it can be put back on tomorrow, and then the old binary is live again.
CANDIDATES=$(
  printf '%s\n' "${PATH:-}" | tr ':' '\n'
  printf '%s\n' /usr/local/bin
)

# A helios binary and not a namesake. `helios version` is cheap, does not touch
# the daemon, and nothing else answers it this way.
is_helios() {
  [ -f "$1" ] && [ -x "$1" ] || return 1
  "$1" version 2> /dev/null | grep -qi '^helios'
}

# Homebrew, apt and friends keep their own records; a file removed behind their
# back leaves them describing a package that is not there.
managed_by_package_manager() {
  case "$1" in
    /opt/homebrew/* | /home/linuxbrew/* | /usr/bin/* | /bin/* | /nix/store/*) return 0 ;;
  esac
  # A Homebrew-linked binary is a symlink into the Cellar.
  case "$(readlink "$1" 2> /dev/null || true)" in
    */Cellar/* | */homebrew/*) return 0 ;;
  esac
  return 1
}

SEEN=''
REMOVED=''
for dir in $CANDIDATES; do
  [ -n "$dir" ] || continue
  old="$dir/helios"

  # The PATH repeats itself, and /usr/local/bin is on it twice by construction.
  case " $SEEN " in *" $old "*) continue ;; esac
  SEEN="$SEEN $old"

  [ "$old" = "$KEEP" ] && continue
  [ -e "$old" ] || continue

  if managed_by_package_manager "$old"; then
    warn "$old belongs to a package manager — remove it with that, not by hand."
    continue
  fi

  if ! is_helios "$old"; then
    warn "$old is on your PATH but does not answer 'helios version' — left alone."
    continue
  fi

  # sudo only where the directory demands it, which is the old /usr/local/bin
  # and little else. Deleting the file of a running daemon is safe on Unix: it
  # keeps running from the inode until it is restarted.
  if [ -w "$dir" ]; then
    rm -f "$old" && REMOVED="$REMOVED $old"
  elif command -v sudo > /dev/null 2>&1; then
    note "Removing the earlier install at $old — $dir needs your password."
    if sudo rm -f "$old"; then
      REMOVED="$REMOVED $old"
    else
      warn "Could not remove $old. It will keep shadowing the new install."
      note "Remove it with:  sudo rm $old"
    fi
  else
    warn "$old is an earlier install and $dir is not writable, with no sudo here."
    note "Remove it with:  rm $old   (as a user who can)"
  fi
done

for old in $REMOVED; do
  note "removed the earlier install at $old"
done
