#!/usr/bin/env bash
# Runs inside the build container started by scripts/build-guacd.sh. Not meant
# to be run on a host: it installs packages and writes to /opt.
#
# Environment (all set by build-guacd.sh): GUACD_VERSION, GUACD_SHA256,
# SNAPSHOT, BUNDLE_NAME, HOST_UID, HOST_GID.

set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
# Anything in the build that stamps a time takes this one, so two builds of
# the same inputs produce the same bytes.
export SOURCE_DATE_EPOCH=1735689600
export TZ=UTC LC_ALL=C.UTF-8

prefix=/opt/helios-guacd
src=/build/src
stage=/build/stage/helios-guacd

# The snapshot is served over HTTPS, and the image has no CA certificates yet,
# so those come from the live archive first. They do not end up in the bundle.
apt-get update -qq
apt-get install -y -qq --no-install-recommends ca-certificates > /dev/null
# snapshot.ubuntu.com is slow and now and then answers 503, so every fetch
# gets retries rather than failing the build on one bad response.
retry=(-o Acquire::Retries=10 -o Acquire::http::Timeout=60 -o Acquire::https::Timeout=60)
apt-get update -qq "${retry[@]}" --snapshot "$SNAPSHOT"
apt-get install -y -qq "${retry[@]}" --no-install-recommends --snapshot "$SNAPSHOT" \
    build-essential pkg-config curl patch patchelf zstd file \
    libcairo2-dev libjpeg-turbo8-dev libpng-dev uuid-dev libssl-dev \
    freerdp3-dev libwinpr3-dev > /dev/null

mkdir -p "$src"
curl -fsSL -o /build/guacd.tar.gz \
    "https://archive.apache.org/dist/guacamole/${GUACD_VERSION}/source/guacamole-server-${GUACD_VERSION}.tar.gz"
echo "${GUACD_SHA256}  /build/guacd.tar.gz" | sha256sum -c -
tar -xzf /build/guacd.tar.gz -C "$src" --strip-components=1

cd "$src"
# Fixes 1.6.0 needs against the FreeRDP in the build image; each patch says
# why in its header.
for p in /build/patches/*.patch; do
    patch -p1 --no-backup-if-mismatch < "$p"
done

# RDP and nothing else: no VNC, SSH, telnet or Kubernetes, and no audio,
# recording or terminal emulator, which only those other protocols need.
./configure --prefix="$prefix" \
    --disable-static --disable-guacenc --disable-guaclog --disable-kubernetes \
    --with-rdp --without-vnc --without-ssh --without-telnet \
    --without-pango --without-terminal --without-pulse --without-vorbis \
    --without-webp --without-websockets \
    --without-libavcodec --without-libavformat --without-libavutil --without-libswscale \
    --with-freerdp-plugin-dir="$prefix/lib/freerdp3" \
    CFLAGS="-O2 -ffile-prefix-map=$src=." \
    CPPFLAGS="-Wno-error=deprecated-declarations" > /dev/null
make -j"$(nproc)" > /dev/null
make install > /dev/null

rm -rf "$stage"
mkdir -p "$stage/sbin" "$stage/lib"
cp "$prefix/sbin/guacd" "$stage/sbin/"
# The client libraries keep their soname links: guacd dlopens
# libguac-client-rdp.so by that exact name.
cp -P "$prefix"/lib/libguac.so* "$prefix"/lib/libguac-client-rdp.so* "$stage/lib/"

# glibc stays behind: it must match the host's loader, and every distro
# newer than the build image has a compatible one.
is_glibc() {
    case "$1" in
        ld-linux* | libc.so.* | libm.so.* | libdl.so.* | libpthread.so.* | \
            librt.so.* | libresolv.so.* | libutil.so.* | libanl.so.*) return 0 ;;
    esac
    return 1
}

# ldd already walks the whole tree, so one pass over the two entry points
# finds every library either of them can pull in.
for bin in "$stage/sbin/guacd" "$stage"/lib/libguac-client-rdp.so.*.*.*; do
    LD_LIBRARY_PATH="$prefix/lib" ldd "$bin" | awk '/=> \//{print $1, $3}'
done | sort -u | while read -r soname path; do
    if is_glibc "$soname" || [ -e "$stage/lib/$soname" ]; then
        continue
    fi
    cp -L "$path" "$stage/lib/$soname"
done

strip --strip-unneeded "$stage/sbin/guacd" "$stage"/lib/*.so* 2> /dev/null || true

patchelf --set-rpath '$ORIGIN/../lib' "$stage/sbin/guacd"
for lib in "$stage"/lib/*; do
    if [ -L "$lib" ]; then
        continue
    fi
    patchelf --set-rpath '$ORIGIN' "$lib"
done

# A loader check from inside the folder alone: with no system copy to fall back
# on, any library the rpaths miss shows up as "not found".
if ldd "$stage/sbin/guacd" | grep -q 'not found'; then
    ldd "$stage/sbin/guacd" >&2
    echo "guacd has unresolved libraries" >&2
    exit 1
fi
"$stage/sbin/guacd" -v

cp "$src/LICENSE" "$src/NOTICE" "$stage/"
chmod -R u=rwX,go=rX "$stage"
find "$stage" -exec touch -h -d "@$SOURCE_DATE_EPOCH" {} +

tar --sort=name --format=gnu --owner=0 --group=0 --numeric-owner \
    --mtime="@$SOURCE_DATE_EPOCH" -C /build/stage -cf - helios-guacd |
    zstd -q -19 -T1 -o "/out/${BUNDLE_NAME}.tar.zst" -f
chown "$HOST_UID:$HOST_GID" "/out/${BUNDLE_NAME}.tar.zst"
