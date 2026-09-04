#!/bin/sh
# Make the shell resolve a just-installed command to the copy that was just
# installed, by writing the PATH line into the shell's own rc file.
#
#   scripts/ensure-path.sh ~/.local/bin helios
#
# The question is not "is this directory on PATH" but "which copy wins", so it
# is asked that way. Older installs elsewhere on the PATH are deleted before
# this runs (scripts/remove-old-installs.sh), leaving the honest case: either
# our copy answers to the name, or the directory is not on the PATH at all.
#
# POSIX sh on purpose, and silent when the right copy already wins.

set -eu

DIR="${1:?usage: ensure-path.sh <directory> [command]}"
CMD="${2:-}"

if [ -t 1 ]; then
  B=$(printf '\033[1m'); DIM=$(printf '\033[2m'); Y=$(printf '\033[33m'); N=$(printf '\033[0m')
else
  B=''; DIM=''; Y=''; N=''
fi
note() { printf '    %s%s%s\n' "$DIM" "$1" "$N"; }
warn() { printf '%s !  %s%s\n' "$Y" "$1" "$N" >&2; }

# What the shell runs today, and whether that is us. Without a command name to
# check there is only the weaker question, so fall back to it.
if [ -n "$CMD" ]; then
  [ "$(command -v "$CMD" 2> /dev/null || true)" = "$DIR/$CMD" ] && exit 0
else
  case ":${PATH:-}:" in *":$DIR:"*) exit 0 ;; esac
fi

# Written as $HOME rather than the expanded path, so the rc file still works on
# a machine where the home directory is somewhere else.
case "$DIR" in
  "$HOME"/*) REF="\$HOME${DIR#"$HOME"}"; SHOWN="~${DIR#"$HOME"}" ;;
  *) REF="$DIR"; SHOWN="$DIR" ;;
esac

# Which file the shell actually reads. Bash on macOS reads .bash_profile for the
# login shell Terminal opens and never .bashrc; on Linux it is the other way
# round for the interactive shells people get.
case "${SHELL##*/}" in
  zsh)
    RC="${ZDOTDIR:-$HOME}/.zshrc"
    LINE="export PATH=\"$REF:\$PATH\""
    RELOAD=.
    ;;
  bash)
    if [ "$(uname -s)" = Darwin ]; then RC="$HOME/.bash_profile"; else RC="$HOME/.bashrc"; fi
    LINE="export PATH=\"$REF:\$PATH\""
    RELOAD=.
    ;;
  fish)
    RC="$HOME/.config/fish/config.fish"
    LINE="fish_add_path $REF"
    # fish 4 dropped `.` as a name for source.
    RELOAD=source
    ;;
  *)
    warn "$SHOWN is not on your PATH, and I do not know where ${SHELL##*/} keeps its rc file."
    note "Add this to it:  export PATH=\"$REF:\$PATH\""
    exit 0
    ;;
esac

RC_SHOWN="~${RC#"$HOME"}"

# Already written, but this shell started before it was.
if [ -f "$RC" ] && grep -qF "$LINE" "$RC"; then
  note "$SHOWN is already on the PATH in $RC_SHOWN — open a new terminal to pick it up."
  exit 0
fi

mkdir -p "$(dirname "$RC")"
if ! { printf '\n# added by helios\n%s\n' "$LINE" >> "$RC"; } 2> /dev/null; then
  warn "$SHOWN is not on your PATH, and $RC_SHOWN could not be written."
  note "Add this to it:  $LINE"
  exit 0
fi

printf '%sAdded %s to your PATH in %s%s\n' "$B" "$SHOWN" "$RC_SHOWN" "$N"
note "Open a new terminal, or run:  $RELOAD $RC_SHOWN"
