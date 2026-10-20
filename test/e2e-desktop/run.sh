#!/usr/bin/env bash
# End-to-end test of the remote desktop (docs/specs/66-remote-desktop.md) in a
# clean Ubuntu 26.04 container with systemd. Nothing touches the host beyond
# Docker: the daemon, xrdp, XFCE and guacd all live in the container, and its
# ports are published on 127.0.0.1 only.
#
#   test/e2e-desktop/run.sh            # build, start, pair, run the probe
#   test/e2e-desktop/run.sh --no-probe # stop after pairing, for manual work
#
# Needs the guacd bundle from scripts/build-guacd.sh in dist/ (or a path in
# HELIOS_GUACD_BUNDLE). The container keeps running at the end so the desktop
# app and the phone can be pointed at it; .out/key.json is the paired device.

set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
out="$here/.out"
name=helios-e2e-desktop
image=helios-e2e-desktop:latest
public_port=27655
internal_port=27654

case "$(uname -m)" in
    x86_64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) echo "unsupported arch $(uname -m)" >&2; exit 1 ;;
esac
bundle="${HELIOS_GUACD_BUNDLE:-$root/dist/helios-guacd-1.6.0-linux-$arch.tar.zst}"
if [ ! -f "$bundle" ]; then
    echo "No guacd bundle at $bundle. Build it: scripts/build-guacd.sh" >&2
    exit 1
fi

mkdir -p "$out"
echo "== build"
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$out/helios" ./cmd/helios)
(cd "$root" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$out/probe-linux" ./test/e2e-desktop/probe)
(cd "$root" && go build -o "$out/probe" ./test/e2e-desktop/probe)
docker build -q -t "$image" "$here" > /dev/null

echo "== container"
docker rm -f "$name" > /dev/null 2>&1 || true
docker run -d --name "$name" --hostname helios-e2e \
    --privileged --cgroupns=private \
    --tmpfs /run --tmpfs /run/lock \
    -p "127.0.0.1:$public_port:7655" \
    -p "127.0.0.1:$internal_port:7656" \
    "$image" > /dev/null
docker exec "$name" systemctl is-system-running --wait > /dev/null 2>&1 || true

docker exec "$name" mkdir -p /opt/helios
docker cp "$out/helios" "$name:/usr/local/bin/helios"
docker cp "$out/probe-linux" "$name:/opt/helios/probe"
docker cp "$bundle" "$name:/opt/helios/guacd.tar.zst"
docker exec "$name" chmod 0755 /usr/local/bin/helios /opt/helios/probe

echo "== daemon"
# The public server binds every interface inside the container so Docker can
# forward to it; the host side of that forward is loopback only. The internal
# server stays on the container's loopback, as it always does, and socat
# carries it to a port Docker can publish.
docker exec -u dev -e HOME=/home/dev -e HELIOS_GUACD_BUNDLE=/opt/helios/guacd.tar.zst -w /home/dev "$name" \
    helios daemon start -d --bind 0.0.0.0
docker exec -d "$name" socat TCP-LISTEN:7656,fork,reuseaddr,bind=0.0.0.0 TCP:127.0.0.1:7654
for _ in $(seq 1 50); do
    if docker exec "$name" curl -fsS http://127.0.0.1:7654/internal/health > /dev/null 2>&1; then
        break
    fi
    sleep 0.2
done

echo "== pair"
docker exec -u dev "$name" /opt/helios/probe pair \
    --internal http://127.0.0.1:7654 --public http://127.0.0.1:7655 \
    --url "http://127.0.0.1:$public_port" --out /home/dev/key.json
docker cp "$name:/home/dev/key.json" "$out/key.json"

if [ "${1:-}" != "--no-probe" ]; then
    echo "== probe"
    "$out/probe" check --key "$out/key.json" --container "$name"
fi

cat <<EOF

The container is still running.
  Daemon (public):   http://127.0.0.1:$public_port
  Daemon (internal): http://127.0.0.1:$internal_port
  Device key:        $out/key.json  (kid + base64url Ed25519 seed)
  Shell:             docker exec -it -u dev $name bash
  Daemon log:        docker exec $name cat /home/dev/.helios/logs/daemon.log
  Stop:              docker rm -f $name
EOF
