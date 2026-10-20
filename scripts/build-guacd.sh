#!/usr/bin/env bash
# Builds the guacd bundle that the daemon installs for remote desktop
# (docs/specs/66-remote-desktop.md, D5).
#
# Ubuntu no longer ships guacd, so Helios builds guacamole-server itself: RDP
# only, against FreeRDP 3, inside a container so the host needs nothing but
# Docker. Every shared library guacd loads, except glibc, is copied into lib/
# and the rpaths point there, so one folder runs on any distro with a glibc at
# least as new as the build image's.
#
# The build is pinned end to end — the image digest, an apt snapshot, the
# source tarball's hash, the patches in scripts/guacd-patches — and the
# archive is written deterministically. That is what lets the daemon embed the
# bundle's SHA-256: the guacd workflow (.github/workflows/guacd.yml) builds the
# bundle and gets the same bytes, so a hash in the source tree can vouch for
# an asset built later.
#
#   ./scripts/build-guacd.sh            # the host's arch
#   ./scripts/build-guacd.sh arm64      # needs an arm64 host or binfmt
#
# Output: dist/helios-guacd-1.6.0-linux-<arch>.tar.zst and its .sha256.

set -euo pipefail

GUACD_VERSION=1.6.0
GUACD_SHA256=8bc45675da96d7b6f39728160181e3d4ff3c08f460f6d26de5805b642bf13f2b

# ubuntu:24.04, the oldest glibc (2.39) the bundle promises to run on. The
# digest is the multi-arch index, so the same pin serves amd64 and arm64.
IMAGE=ubuntu@sha256:68ee22004a9109b44973313b02ff7b1de250f1de0f5ecb628d73f579aaff33ac

# The archive as it stood at this moment. Moving it is how a FreeRDP security
# fix reaches the bundle, and it changes the bundle's hash, which is the point.
SNAPSHOT=20261015T000000Z

root="$(cd "$(dirname "$0")/.." && pwd)"

arch="${1:-}"
if [ -z "$arch" ]; then
    case "$(uname -m)" in
        x86_64) arch=amd64 ;;
        aarch64 | arm64) arch=arm64 ;;
        *)
            echo "Unsupported host arch: $(uname -m)" >&2
            exit 1
            ;;
    esac
fi
case "$arch" in
    amd64 | arm64) ;;
    *)
        printf 'Usage: %s [amd64|arm64]\n' "$0" >&2
        exit 1
        ;;
esac

mkdir -p "$root/dist"
name="helios-guacd-${GUACD_VERSION}-linux-${arch}"

docker run --rm \
    --platform "linux/${arch}" \
    -e GUACD_VERSION="$GUACD_VERSION" \
    -e GUACD_SHA256="$GUACD_SHA256" \
    -e SNAPSHOT="$SNAPSHOT" \
    -e BUNDLE_NAME="$name" \
    -e HOST_UID="$(id -u)" \
    -e HOST_GID="$(id -g)" \
    -v "$root/scripts/guacd-bundle.sh:/build/guacd-bundle.sh:ro" \
    -v "$root/scripts/guacd-patches:/build/patches:ro" \
    -v "$root/dist:/out" \
    "$IMAGE" bash /build/guacd-bundle.sh

(cd "$root/dist" && sha256sum "$name.tar.zst" > "$name.tar.zst.sha256")
cat "$root/dist/$name.tar.zst.sha256"
