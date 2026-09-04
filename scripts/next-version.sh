#!/usr/bin/env bash
# The number the next release will carry, worked out rather than typed.
#
# The repo holds no version. The newest release does, and this walks one step
# past it at the level asked for. That is the whole rule: the step is always
# one, and everything to the right of it goes to zero. A number nobody types is
# a number nobody can mistype, and one derived from the newest release cannot
# collide with a release that already exists.
#
#   ./scripts/next-version.sh patch   # 3.11.1 -> 3.11.2
#   ./scripts/next-version.sh minor   # 3.11.1 -> 3.12.0
#   ./scripts/next-version.sh major   # 3.11.1 -> 4.0.0

set -euo pipefail

REPO=kamrul1157024/helios

bump="${1:-patch}"
case "$bump" in
    patch | minor | major) ;;
    *)
        printf 'Usage: %s [patch|minor|major]\n' "$0" >&2
        exit 1
        ;;
esac

# The newest release, not the newest tag: a tag can be a prerelease, or one
# pushed by mistake, and neither is the number people are running. An
# unreachable GitHub is fatal here — a release built on a guessed base is a
# release with the wrong number on it.
if ! released=$(gh release view --repo "$REPO" --json tagName -q .tagName 2> /dev/null); then
    printf 'Error: could not reach GitHub for the newest release of %s.\n' "$REPO" >&2
    exit 1
fi
released=${released#v}

# A repo with no releases yet starts from nothing, so the first release is
# whichever component was asked for, moved off zero.
[ -n "$released" ] || released=0.0.0

if ! printf '%s' "$released" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
    printf 'Error: newest release is "%s", which is not a x.y.z number.\n' "$released" >&2
    exit 1
fi

IFS=. read -r major minor patch <<< "$released"

case "$bump" in
    major) printf '%d.0.0\n' "$((major + 1))" ;;
    minor) printf '%d.%d.0\n' "$major" "$((minor + 1))" ;;
    patch) printf '%d.%d.%d\n' "$major" "$minor" "$((patch + 1))" ;;
esac
