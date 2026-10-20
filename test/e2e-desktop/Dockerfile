# A clean Ubuntu 26.04 host for the remote desktop e2e
# (docs/specs/66-remote-desktop.md, Testing). systemd runs as PID 1 because
# the install enables xrdp as a service, exactly as on a real machine.
#
# Nothing of the desktop is preinstalled: the test starts from "missing" and
# the daemon installs xrdp, XFCE and guacd itself. The extras below are the
# test's own tools — xdpyinfo to prove an agent can reach the display, ss to
# check what listens where, socat to expose the daemon's loopback-only
# internal port to the host — and a stand-in for the claude CLI.
FROM ubuntu:26.04

ENV container=docker DEBIAN_FRONTEND=noninteractive

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        systemd systemd-sysv dbus sudo ca-certificates curl \
        x11-utils iproute2 procps socat \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Units that have nothing to do in a container and only fail there.
RUN systemctl mask systemd-udevd.service systemd-udev-trigger.service \
        systemd-modules-load.service getty.target console-getty.service

RUN useradd -m -s /bin/bash dev \
    && echo 'dev ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/dev \
    && chmod 0440 /etc/sudoers.d/dev

COPY fake-claude /usr/local/bin/claude
RUN chmod 0755 /usr/local/bin/claude

STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
