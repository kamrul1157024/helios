#!/bin/sh
# Install Helios: the daemon, and the desktop app if this machine can build it.
#
#   curl -fsSL https://raw.githubusercontent.com/kamrul1157024/helios/main/scripts/install.sh | sh
#
# There are no prebuilt daemon binaries, so this builds from source. It needs Go
# for the daemon and Node for the app, and if either is missing it says exactly
# how to get it rather than failing with a compiler error.
#
# POSIX sh on purpose: piped into `sh` on Debian this is dash, not bash.
#
# Knobs, for a test run that touches nothing real:
#   HELIOS_PREFIX   where the binary goes         (default ~/.local/bin)
#   HELIOS_APP_DIR  where the macOS app goes      (default /Applications)
#   HELIOS_SRC      where the checkout lives      (default ~/.helios/src)
#   HELIOS_REF      what to build                 (default the newest release)

set -eu

REPO_URL="https://github.com/kamrul1157024/helios.git"
GO_MIN_MINOR=26   # go1.26+, matching go.mod
NODE_MIN=22       # matching .nvmrc and the Makefile

# A directory the user already owns, so the install has nothing to ask for.
# Point it at /usr/local/bin and it still works — that path wants a password.
PREFIX="${HELIOS_PREFIX:-$HOME/.local/bin}"
APP_DIR="${HELIOS_APP_DIR:-/Applications}"
SRC="${HELIOS_SRC:-$HOME/.helios/src}"

# Colour only when someone is watching. Piped into a file it would be noise.
if [ -t 1 ]; then
  B=$(printf '\033[1m'); DIM=$(printf '\033[2m'); Y=$(printf '\033[33m'); R=$(printf '\033[31m'); N=$(printf '\033[0m')
else
  B=''; DIM=''; Y=''; R=''; N=''
fi

step() { printf '%s==>%s %s\n' "$B" "$N" "$1"; }
note() { printf '    %s%s%s\n' "$DIM" "$1" "$N"; }
warn() { printf '%s !  %s%s\n' "$Y" "$1" "$N"; }
fail() { printf '%s !  %s%s\n' "$R" "$1" "$N" >&2; exit 1; }

# Whether there is a person here to answer. Opening it is the only honest test:
# /dev/tty exists on a machine with no controlling terminal too, and reading it
# there fails with "Device not configured".
has_tty() { (exec < /dev/tty) 2> /dev/null; }

# A question the pipe cannot answer. Stdin is the script itself, so the terminal
# has to be opened by name — and when there is no terminal the default stands.
ask() {
  question="$1"; default="$2"
  if ! has_tty; then
    note "$question — assuming $default"
    [ "$default" = y ]
    return
  fi
  printf '    %s [y/n] ' "$question" > /dev/tty
  read -r reply < /dev/tty || reply=""
  [ -z "$reply" ] && reply="$default"
  case "$reply" in y | Y | yes | YES) return 0 ;; *) return 1 ;; esac
}

have() { command -v "$1" > /dev/null 2>&1; }

# ─── Platform ───────────────────────────────────────────────────────────────

OS=$(uname -s)
case "$OS" in
  Darwin) PLATFORM=macos ;;
  Linux) PLATFORM=linux ;;
  *) fail "Helios runs on macOS and Linux; this machine reports $OS." ;;
esac

printf '\n%sHelios%s — building from source and installing.\n\n' "$B" "$N"

# ─── What this needs ────────────────────────────────────────────────────────

# How to get a missing tool on this machine, in the words of its own package
# manager. Guessing wrong is worse than saying nothing, so an unknown Linux is
# pointed at the project's own download page.
howto() {
  what="$1"; brew="$2"; apt="$3"; url="$4"
  if [ "$PLATFORM" = macos ] && have brew; then
    note "install it with:  brew install $brew"
  elif have apt-get; then
    note "install it with:  sudo apt install $apt"
  fi
  note "or download $what from $url"
}

have git || {
  warn "git is missing, and the source has to be fetched with it."
  howto git git git "https://git-scm.com/downloads"
  exit 1
}

# The Go the daemon needs. `go1.26.1` and `go1.30` both pass; `go1.24` does not.
go_ok() {
  have go || return 1
  v=$(go env GOVERSION 2>/dev/null || echo '')
  minor=$(echo "$v" | sed -n 's/^go1\.\([0-9][0-9]*\).*/\1/p')
  [ -n "$minor" ] && [ "$minor" -ge "$GO_MIN_MINOR" ]
}

if ! go_ok; then
  if have go; then
    warn "Go $(go env GOVERSION 2>/dev/null) is older than the go1.$GO_MIN_MINOR the daemon needs."
  else
    warn "Go is missing, and the daemon is written in it."
  fi
  howto Go go golang-go "https://go.dev/dl/"
  exit 1
fi

node_ok() {
  have npm || return 1
  have node || return 1
  major=$(node -v 2>/dev/null | sed -n 's/^v\([0-9][0-9]*\).*/\1/p')
  [ -n "$major" ] && [ "$major" -ge "$NODE_MIN" ]
}

WITH_DESKTOP=yes
if ! node_ok; then
  if have node; then
    warn "Node $(node -v 2>/dev/null) is older than the v$NODE_MIN the desktop app is built with."
  else
    warn "Node is missing, so the desktop app cannot be built here."
  fi
  howto Node node nodejs "https://nodejs.org/en/download"
  note "The daemon does not need it — the app is a client, and you can add it later."
  if ask "Install the daemon on its own and carry on?" y; then
    WITH_DESKTOP=no
  else
    exit 1
  fi
fi

# ─── Source ─────────────────────────────────────────────────────────────────

# Said once the checks are in, so it names what will actually happen rather than
# promising an app this machine cannot build.
note "daemon  → $PREFIX/helios"
[ "$WITH_DESKTOP" = yes ] && [ "$PLATFORM" = macos ] && note "app     → $APP_DIR/Helios.app"
[ "$WITH_DESKTOP" = yes ] && [ "$PLATFORM" = linux ] && note "app     → desktop package"
note "source  → $SRC"
printf '\n'

step "Fetching the source"
if [ -d "$SRC/.git" ]; then
  note "updating $SRC"
  git -C "$SRC" fetch --tags --quiet origin
else
  mkdir -p "$(dirname "$SRC")"
  git clone --quiet "$REPO_URL" "$SRC"
fi

# The newest published release, which is not the newest tag: a tag can be a
# prerelease or one pushed by mistake, and this endpoint skips both. A clone
# that cannot reach GitHub still has its tags, and falls back to them.
latest_release() {
  have curl || return 1
  curl -fsSL "https://api.github.com/repos/kamrul1157024/helios/releases/latest" 2> /dev/null |
    sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1 | grep . || return 1
}

REF="${HELIOS_REF:-$(latest_release || git -C "$SRC" describe --tags --abbrev=0 origin/main 2> /dev/null || echo main)}"
git -C "$SRC" checkout --quiet --detach "$REF"
note "building $REF"

# ─── The daemon ─────────────────────────────────────────────────────────────

step "Building the daemon"
# Stamped, or the daemon reports "dev" and every client reads that as a build
# nobody should be nagged about — including one that is genuinely years behind.
# On a release it is the tag; on main it is tag-commits-sha, which is honest.
STAMP=$(git -C "$SRC" describe --tags 2> /dev/null | sed 's/^v//')
[ -n "$STAMP" ] || STAMP=dev
(cd "$SRC" && go build -ldflags "-X github.com/kamrul1157024/helios/internal/version.Current=$STAMP" -o helios ./cmd/helios/)
[ "$PLATFORM" = macos ] && codesign -s - -f "$SRC/helios" > /dev/null 2>&1

install_daemon() {
  dest="$1"
  mkdir -p "$dest"
  # Linux returns ETXTBSY when overwriting a binary that is currently mapped —
  # a running daemon — so the new one is renamed over the old.
  $2 cp "$SRC/helios" "$dest/helios.new"
  $2 mv -f "$dest/helios.new" "$dest/helios"
  if [ "$PLATFORM" = macos ]; then
    $2 codesign -s - -f "$dest/helios" > /dev/null 2>&1 || true
    $2 xattr -d com.apple.quarantine "$dest/helios" > /dev/null 2>&1 || true
  fi
}

step "Installing the daemon to $PREFIX"
# The default needs no permission. This only bites when HELIOS_PREFIX names
# somewhere the system owns, and then it is asked for plainly rather than
# quietly installing somewhere else and leaving two copies behind.
SUDO=''
if ! mkdir -p "$PREFIX" 2> /dev/null || [ ! -w "$PREFIX" ]; then
  have sudo || fail "$PREFIX is not writable and there is no sudo here. Set HELIOS_PREFIX to a directory you own."
  note "$PREFIX belongs to the system, so this step needs your password."
  note "It copies a single file: $PREFIX/helios. Nothing else is touched."
  sudo -v || fail "Without that, $PREFIX cannot be written. Set HELIOS_PREFIX to a directory you own."
  SUDO=sudo
fi
install_daemon "$PREFIX" "$SUDO"
BIN="$PREFIX/helios"
note "installed $BIN"

# The rest of what an install owes you: one copy on the machine, on a PATH the
# shell reads, and a daemon running the build that was just fetched. Guarded
# because $SRC sits on the newest release, and a release older than these
# scripts does not carry them — this file is fetched from main.
[ -f "$SRC/scripts/remove-old-installs.sh" ] && sh "$SRC/scripts/remove-old-installs.sh" "$BIN"
[ -f "$SRC/scripts/ensure-path.sh" ] && sh "$SRC/scripts/ensure-path.sh" "$PREFIX" helios
[ -f "$SRC/scripts/restart-daemon.sh" ] && sh "$SRC/scripts/restart-daemon.sh" "$BIN"

# ─── The desktop app ────────────────────────────────────────────────────────

APP=''
if [ "$WITH_DESKTOP" = yes ]; then
  step "Building the desktop app (a few minutes the first time)"
  # Electron's build talks a great deal and none of it is news. It is kept in
  # full, in a file, and only pointed at if the build does not finish.
  LOG="${TMPDIR:-/tmp}/helios-desktop-build.log"
  if ! (cd "$SRC" && make desktop-app) > "$LOG" 2>&1; then
    warn "The desktop app did not build. The whole log is at $LOG"
    warn "The daemon is installed and works without it."
    tail -5 "$LOG"
    WITH_DESKTOP=no
  fi
fi

if [ "$WITH_DESKTOP" = yes ]; then
  if [ "$PLATFORM" = macos ]; then
    BUNDLE=$(find "$SRC/desktop/release" -maxdepth 2 -name 'Helios.app' -print 2> /dev/null | head -1)
    if [ -n "$BUNDLE" ]; then
      mkdir -p "$APP_DIR"
      # Replaced rather than copied into: a copy leaves the last build's files
      # behind inside the bundle.
      rm -rf "$APP_DIR/Helios.app"
      cp -R "$BUNDLE" "$APP_DIR/Helios.app"
      xattr -dr com.apple.quarantine "$APP_DIR/Helios.app" > /dev/null 2>&1 || true
      APP="$APP_DIR/Helios.app"
    else
      warn "The app built, but no Helios.app came out of it — see $SRC/desktop/release."
    fi
  else
    DEB=$(find "$SRC/desktop/release" -maxdepth 1 -name '*.deb' 2> /dev/null | head -1)
    IMG=$(find "$SRC/desktop/release" -maxdepth 1 -name '*.AppImage' 2> /dev/null | head -1)
    if [ -n "$DEB" ] && have apt-get && have sudo; then
      note "Installing the .deb needs your password."
      sudo apt-get install -y "$DEB" > /dev/null && APP='helios-desktop'
    elif [ -n "$IMG" ]; then
      mkdir -p "$HOME/.local/bin"
      cp "$IMG" "$HOME/.local/bin/helios-desktop"
      chmod +x "$HOME/.local/bin/helios-desktop"
      APP="$HOME/.local/bin/helios-desktop"
    else
      warn "The app built, but there is nothing installable in $SRC/desktop/release."
    fi
  fi
  [ -n "$APP" ] && note "installed $APP"
fi

# ─── A way in from your phone ───────────────────────────────────────────────

# Any one of these is enough, so the note is only worth printing when there is
# none. The list mirrors the TUI's own hints (internal/tui/start.go).
TUNNEL=''
for t in tailscale cloudflared ngrok zrok lt loclx; do
  have "$t" && TUNNEL="$t" && break
done

if [ -n "$TUNNEL" ]; then
  step "Found $TUNNEL — helios start will offer it as your tunnel"
else
  step "One thing left: a tunnel, so your phone can reach this daemon"
  note "Tailscale is the recommendation. It keeps the daemon inside your tailnet and"
  note "the hostname never changes:"
  note "  tailscale     https://tailscale.com/download          (brew install tailscale)"
  note "Or a public URL, if you would rather not run a VPN on the phone:"
  note "  cloudflared   https://developers.cloudflare.com/cloudflare-one/connections/connect-networks/downloads/"
  note "  ngrok         https://ngrok.com/download              (brew install ngrok)"
  note "  zrok          https://zrok.io                         (brew install openziti/tap/zrok)"
  note "  localtunnel   npm install -g localtunnel"
  note "helios start checks again, and there are nine providers in the picker."
fi

# ─── Staying awake ──────────────────────────────────────────────────────────

# A sleeping Mac is a stopped session, which is a surprising way to lose an hour
# of an agent's work. Raised only when this Mac actually sleeps on power and
# nothing is already holding it open.
if [ "$PLATFORM" = macos ] && ! have v-claw; then
  AC_SLEEP=$(pmset -g custom 2> /dev/null | awk '/AC Power/ { ac = 1 } ac && $1 == "sleep" { print $2; exit }')
  if [ -n "$AC_SLEEP" ] && [ "$AC_SLEEP" != 0 ]; then
    step "This Mac sleeps after $AC_SLEEP minutes on power, and sessions stop when it does"
    note "v-claw holds it awake while the adapter is in — lid shut included — and lets"
    note "go the moment you unplug: https://github.com/kamrul1157024/v-claw"
    if ask "Install v-claw? It builds from source and asks for your password once." n; then
      if ! xcode-select -p > /dev/null 2>&1; then
        warn "It needs the Xcode command line tools first:  xcode-select --install"
      else
        VCLAW="$(dirname "$SRC")/v-claw"
        if [ -d "$VCLAW/.git" ]; then
          # A checkout someone has been working in should not stop the install.
          git -C "$VCLAW" pull --quiet --ff-only || warn "Could not update $VCLAW — building what is there."
        else
          git clone --quiet https://github.com/kamrul1157024/v-claw "$VCLAW"
        fi
        VLOG="${TMPDIR:-/tmp}/v-claw-install.log"
        if (cd "$VCLAW" && make install) > "$VLOG" 2>&1; then
          note "installed v-claw — the claw in your menu bar says when it is holding"
        else
          warn "v-claw did not install. The log is at $VLOG"
        fi
      fi
    else
      note "Or, without installing anything:"
      note "  caffeinate -s helios start    awake for as long as that runs"
      note "  sudo pmset -c sleep 0         never sleep on power, until you set it back"
    fi
  fi
fi

# ─── Over to you ────────────────────────────────────────────────────────────

printf '\n%sDone.%s\n' "$B" "$N"
note "daemon  $BIN"
[ -n "$APP" ] && note "app     $APP"
printf '\n'

if has_tty; then
  step "Starting the setup TUI"
  exec "$BIN" start < /dev/tty
else
  step "Run this next:  helios start"
fi
