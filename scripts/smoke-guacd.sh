#!/usr/bin/env bash
# Starts guacd from a bundle in a clean ubuntu:26.04 container and checks that
# it listens and answers the Guacamole handshake. The container has none of
# the bundle's libraries, so this is the test that the bundle needs nothing
# from the host but glibc (docs/specs/66-remote-desktop.md, D5).
#
#   ./scripts/smoke-guacd.sh dist/helios-guacd-1.6.0-linux-amd64.tar.zst [amd64|arm64]

set -euo pipefail

bundle="${1:?usage: $0 <bundle.tar.zst> [arch]}"
arch="${2:-}"
bundle="$(cd "$(dirname "$bundle")" && pwd)/$(basename "$bundle")"
platform=()
if [ -n "$arch" ]; then
    platform=(--platform "linux/$arch")
fi

# The script runs as a non-root user, as the daemon runs guacd. It sends
# "select rdp" and expects guacd's "args" back, which proves the RDP plugin
# and every library behind it load.
docker run --rm "${platform[@]}" -e DEBIAN_FRONTEND=noninteractive -v "$bundle:/bundle.tar.zst:ro" ubuntu:26.04 bash -euc '
    apt-get update -qq > /dev/null
    apt-get install -y -qq --no-install-recommends zstd python3 > /dev/null
    useradd -m smoke
    su smoke -c "
        set -eu
        mkdir -p ~/guacd
        tar --zstd -xf /bundle.tar.zst -C ~/guacd --strip-components=1
        ~/guacd/sbin/guacd -v
        ~/guacd/sbin/guacd -f -b 127.0.0.1 -l 4822 -L debug > ~/guacd.log 2>&1 &
        python3 - <<EOF
import socket, sys, time
deadline = time.time() + 10
while True:
    try:
        s = socket.create_connection((\"127.0.0.1\", 4822), timeout=2)
        break
    except OSError:
        if time.time() > deadline:
            sys.exit(\"guacd is not listening on 127.0.0.1:4822\")
        time.sleep(0.2)
s.sendall(b\"6.select,3.rdp;\")
s.settimeout(10)
data = b\"\"
while not data.endswith(b\";\"):
    chunk = s.recv(65536)
    if not chunk:
        break
    data += chunk
if not data.startswith(b\"4.args,\"):
    sys.exit(\"unexpected answer: %r\" % data[:200])
print(\"guacd answered select rdp with %d args\" % data.count(b\",\"))
EOF
    "
'
