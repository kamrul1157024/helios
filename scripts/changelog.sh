#!/usr/bin/env bash
# Generate a changelog from conventional commits between two git refs.
# Usage: ./scripts/changelog.sh [from_ref] [to_ref]
#   from_ref: defaults to the most recent tag before to_ref
#   to_ref:   defaults to HEAD

set -euo pipefail

TO_REF="${2:-HEAD}"
FROM_REF="${1:-}"

# If no from_ref, find the previous tag
if [ -z "$FROM_REF" ]; then
    FROM_REF=$(git describe --tags --abbrev=0 "$TO_REF" 2>/dev/null || true)
    if [ -z "$FROM_REF" ]; then
        FROM_REF=$(git rev-list --max-parents=0 HEAD)
    fi
fi

RANGE="${FROM_REF}..${TO_REF}"
OUTPUT=""

print_section() {
    local type="$1"
    local heading="$2"
    local commits
    commits=$(git log "$RANGE" --pretty=format:"- %s (%h)" --no-merges \
        | grep -E "^- ${type}(\(.*\))?:" || true)
    if [ -n "$commits" ]; then
        local cleaned
        cleaned=$(echo "$commits" | sed -E "s/^- ${type}(\([^)]*\))?: /- /")
        OUTPUT+="### ${heading}"$'\n'"${cleaned}"$'\n\n'
    fi
}

print_section "feat"     "Features"
print_section "fix"      "Bug Fixes"
print_section "refactor" "Refactoring"
print_section "docs"     "Documentation"
print_section "test"     "Tests"
print_section "ci"       "CI"
print_section "chore"    "Chores"

# Catch commits that don't follow conventional commit format
other=$(git log "$RANGE" --pretty=format:"- %s (%h)" --no-merges \
    | grep -vE "^- (feat|fix|refactor|docs|test|ci|chore)(\(.*\))?:" || true)
if [ -n "$other" ]; then
    OUTPUT+="### Other"$'\n'"${other}"$'\n\n'
fi

if [ -z "$OUTPUT" ]; then
    echo "No changes since ${FROM_REF}."
else
    printf '%s' "$OUTPUT"
fi
