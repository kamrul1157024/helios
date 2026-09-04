#!/bin/sh
# Tests for the three scripts `make install` runs after the binary is copied.
#
#   sh scripts/install_test.sh
#
# They are worth testing because they are the parts that touch a machine rather
# than a build directory: one deletes files, one edits a shell rc, one restarts
# a service. Every case here runs against a temporary HOME and temporary
# directories, so this is safe to run on a laptop.
#
# What is deliberately not covered: removing the real /usr/local/bin/helios,
# which cannot be done to a developer's machine to prove a point. The CI job
# runs the whole installer on a disposable runner and checks that instead.

set -eu

HERE=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
PASSED=0
FAILED=0

pass() { PASSED=$((PASSED + 1)); printf '  ok    %s\n' "$1"; }
fail() { FAILED=$((FAILED + 1)); printf '  FAIL  %s\n' "$1"; }

# Every test gets its own throwaway HOME, and none of them inherit the caller's.
sandbox() {
  BOX=$(mktemp -d)
  HOME="$BOX/home"
  mkdir -p "$HOME"
  export HOME
}
cleanup() { [ -n "${BOX:-}" ] && rm -rf "$BOX"; }
trap cleanup EXIT

# A binary that answers `helios version` the way the real one does. The removal
# script uses that answer to decide what it is allowed to delete.
plant_helios() {
  mkdir -p "$(dirname "$1")"
  printf '#!/bin/sh\n[ "$1" = version ] && echo "helios v0.0.0-test"\nexit 0\n' > "$1"
  chmod +x "$1"
}

printf '\nremove-old-installs.sh\n'

sandbox
plant_helios "$BOX/old/helios"
plant_helios "$BOX/new/helios"
PATH="$BOX/old:$BOX/new:/usr/bin:/bin" sh "$HERE/remove-old-installs.sh" "$BOX/new/helios" > /dev/null 2>&1
[ ! -e "$BOX/old/helios" ] && pass "deletes an earlier install on the PATH" || fail "deletes an earlier install on the PATH"
[ -e "$BOX/new/helios" ] && pass "keeps the install it was told to keep" || fail "keeps the install it was told to keep"
cleanup

# Someone's own script called helios is not ours to delete.
sandbox
mkdir -p "$BOX/mine"
printf '#!/bin/sh\necho my own thing\n' > "$BOX/mine/helios"
chmod +x "$BOX/mine/helios"
plant_helios "$BOX/new/helios"
PATH="$BOX/mine:$BOX/new:/usr/bin:/bin" sh "$HERE/remove-old-installs.sh" "$BOX/new/helios" > /dev/null 2>&1
[ -e "$BOX/mine/helios" ] && pass "keeps a namesake that is not a helios binary" || fail "keeps a namesake that is not a helios binary"
cleanup

# A Homebrew-linked copy belongs to Homebrew.
sandbox
plant_helios "$BOX/brew/Cellar/helios/1.0/bin/helios"
mkdir -p "$BOX/brewbin"
ln -s "$BOX/brew/Cellar/helios/1.0/bin/helios" "$BOX/brewbin/helios"
plant_helios "$BOX/new/helios"
PATH="$BOX/brewbin:$BOX/new:/usr/bin:/bin" sh "$HERE/remove-old-installs.sh" "$BOX/new/helios" > /dev/null 2>&1
[ -e "$BOX/brewbin/helios" ] && pass "keeps a copy a package manager owns" || fail "keeps a copy a package manager owns"
cleanup

printf '\nensure-path.sh\n'

# The right copy already wins, so there is nothing to say and nothing to write.
sandbox
plant_helios "$BOX/bin/helios"
OUT=$(PATH="$BOX/bin:/usr/bin:/bin" SHELL=/bin/zsh sh "$HERE/ensure-path.sh" "$BOX/bin" helios 2>&1)
[ -z "$OUT" ] && pass "silent when the installed copy already answers" || fail "silent when the installed copy already answers (said: $OUT)"
[ ! -e "$HOME/.zshrc" ] && pass "writes nothing when there is nothing to fix" || fail "writes nothing when there is nothing to fix"
cleanup

sandbox
PATH=/usr/bin:/bin SHELL=/bin/zsh sh "$HERE/ensure-path.sh" "$HOME/.local/bin" helios > /dev/null 2>&1
grep -q 'export PATH="$HOME/.local/bin:$PATH"' "$HOME/.zshrc" 2> /dev/null &&
  pass "writes the PATH line to .zshrc" || fail "writes the PATH line to .zshrc"
# Written as $HOME, not as this machine's home directory.
grep -q "$HOME/.local" "$HOME/.zshrc" 2> /dev/null &&
  fail "writes \$HOME rather than the expanded path" || pass "writes \$HOME rather than the expanded path"
# Twice must not mean two lines.
PATH=/usr/bin:/bin SHELL=/bin/zsh sh "$HERE/ensure-path.sh" "$HOME/.local/bin" helios > /dev/null 2>&1
[ "$(grep -c 'added by helios' "$HOME/.zshrc")" = 1 ] &&
  pass "does not write the line twice" || fail "does not write the line twice"
cleanup

sandbox
PATH=/usr/bin:/bin SHELL=/bin/fish sh "$HERE/ensure-path.sh" "$HOME/.local/bin" helios > /dev/null 2>&1
grep -q 'fish_add_path' "$HOME/.config/fish/config.fish" 2> /dev/null &&
  pass "uses fish_add_path in config.fish" || fail "uses fish_add_path in config.fish"
cleanup

# Bash reads a different file depending on the platform, and getting it wrong
# means writing a line nobody's shell ever reads.
sandbox
PATH=/usr/bin:/bin SHELL=/bin/bash sh "$HERE/ensure-path.sh" "$HOME/.local/bin" helios > /dev/null 2>&1
if [ "$(uname -s)" = Darwin ]; then
  [ -f "$HOME/.bash_profile" ] && pass "bash on macOS writes .bash_profile" || fail "bash on macOS writes .bash_profile"
else
  [ -f "$HOME/.bashrc" ] && pass "bash on Linux writes .bashrc" || fail "bash on Linux writes .bashrc"
fi
cleanup

sandbox
OUT=$(PATH=/usr/bin:/bin SHELL=/bin/nonesuch sh "$HERE/ensure-path.sh" "$HOME/.local/bin" helios 2>&1 || true)
case "$OUT" in
  *"export PATH"*) pass "an unknown shell is told what to add by hand" ;;
  *) fail "an unknown shell is told what to add by hand (said: $OUT)" ;;
esac
cleanup

printf '\nrestart-daemon.sh\n'

# A stub standing in for the installed binary, which logs what it was asked to
# do. The real one is not involved: this is about the decision, not the daemon.
stub() {
  cat > "$BOX/helios" <<EOF
#!/bin/sh
echo "\$*" >> "$BOX/calls"
case "\$*" in
  "daemon status") echo "$1" ;;
  "daemon stop") exit $2 ;;
esac
exit 0
EOF
  chmod +x "$BOX/helios"
  : > "$BOX/calls"
}

sandbox
stub "helios daemon is running (pid 1)" 0
sh "$HERE/restart-daemon.sh" "$BOX/helios" > /dev/null 2>&1
grep -q 'daemon stop' "$BOX/calls" && grep -q 'daemon start -d' "$BOX/calls" &&
  pass "restarts a daemon that is running" || fail "restarts a daemon that is running"
cleanup

sandbox
stub "helios daemon is not running" 0
sh "$HERE/restart-daemon.sh" "$BOX/helios" > /dev/null 2>&1
grep -q 'daemon stop' "$BOX/calls" &&
  fail "leaves a stopped daemon alone" || pass "leaves a stopped daemon alone"
cleanup

# Starting after a failed stop would be a second daemon, not a restart.
sandbox
stub "helios daemon is running (pid 1)" 1
sh "$HERE/restart-daemon.sh" "$BOX/helios" > /dev/null 2>&1
grep -q 'daemon start' "$BOX/calls" &&
  fail "does not start when the stop failed" || pass "does not start when the stop failed"
cleanup

printf '\n%s passed, %s failed\n\n' "$PASSED" "$FAILED"
[ "$FAILED" -eq 0 ]
