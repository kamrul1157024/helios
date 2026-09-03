#!/usr/bin/env bash
# The one version number, checked — and with --fix, corrected.
#
# `VERSION` holds the number the next release will carry. The daemon is stamped
# from git at build time, but the desktop and mobile clients each keep a copy in
# their own manifest, and a copy is a thing that drifts.
#
# The number only ever has to be ahead of the newest release. Nobody has to
# remember to move it: CI runs this with --fix on every PR and pushes the bump
# back, which is what keeps a revert or a hand-edited manifest from turning into
# a release nobody can install over.
#
#   ./scripts/check-version.sh        # report, exit 1 if anything is off
#   ./scripts/check-version.sh --fix  # write the number everywhere it belongs

set -euo pipefail

cd "$(dirname "$0")/.."

FIX=no
[ "${1:-}" = "--fix" ] && FIX=yes

declared=$(tr -d '[:space:]' < VERSION)
desktop=$(sed -n 's/.*"version": "\(.*\)".*/\1/p' desktop/package.json | head -1)
# Flutter's version is name+build; only the name is the release number.
mobile=$(sed -n 's/^version: *\([^+]*\).*/\1/p' mobile/pubspec.yaml | tr -d '[:space:]')

# The newest release, not the newest tag: a tag can be a prerelease or one
# pushed by mistake. Unreachable GitHub leaves the number where it is rather
# than failing a PR over a network problem.
released=$(gh release view --repo kamrul1157024/helios --json tagName -q .tagName 2> /dev/null | sed 's/^v//' || true)

# The number this branch should carry: whatever it already declares, as long as
# that is past the newest release. Otherwise the release's patch, plus one —
# incremental on purpose, so a revert lands on a number no release has taken.
target="$declared"
if [ -n "$released" ]; then
    ahead=$(printf '%s\n%s\n' "$declared" "$released" | sort -V | tail -1)
    if [ "$declared" = "$released" ] || [ "$ahead" != "$declared" ]; then
        target=$(printf '%s' "$released" | awk -F. '{ printf "%d.%d.%d", $1, $2, $3 + 1 }')
    fi
else
    printf '  (could not reach GitHub for the newest release — leaving %s alone)\n' "$declared"
fi

if [ "$FIX" = no ]; then
    status=0
    [ "$declared" = "$target" ] ||
        { printf '✗ VERSION is %s, but %s is already released — it should be %s.\n' "$declared" "$released" "$target" >&2; status=1; }
    [ "$desktop" = "$target" ] ||
        { printf '✗ desktop/package.json says %s, not %s.\n' "$desktop" "$target" >&2; status=1; }
    [ "$mobile" = "$target" ] ||
        { printf '✗ mobile/pubspec.yaml says %s, not %s.\n' "$mobile" "$target" >&2; status=1; }
    if [ "$status" != 0 ]; then
        printf '\nRun ./scripts/check-version.sh --fix\n' >&2
        exit 1
    fi
    printf '✓ version %s\n' "$target"
    exit 0
fi

# ─── --fix ──────────────────────────────────────────────────────────────────

# sed -i is spelled differently on macOS and on the runners, so the file is
# rewritten rather than edited in place.
rewrite() {
    file="$1"
    script="$2"
    sed "$script" "$file" > "$file.tmp"
    mv "$file.tmp" "$file"
}

changed=no

if [ "$declared" != "$target" ]; then
    printf '%s\n' "$target" > VERSION
    printf '  VERSION %s → %s\n' "$declared" "$target"
    changed=yes
fi
if [ "$desktop" != "$target" ]; then
    rewrite desktop/package.json "s/\"version\": \"$desktop\"/\"version\": \"$target\"/"
    printf '  desktop/package.json %s → %s\n' "$desktop" "$target"
    changed=yes
fi
if [ "$mobile" != "$target" ]; then
    # The build number after the + is Android's, and counts separately.
    rewrite mobile/pubspec.yaml "s/^version: *$mobile+/version: $target+/"
    printf '  mobile/pubspec.yaml %s → %s\n' "$mobile" "$target"
    changed=yes
fi

[ "$changed" = yes ] || printf '✓ version %s\n' "$target"
