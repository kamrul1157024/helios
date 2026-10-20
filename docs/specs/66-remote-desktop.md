# Remote Desktop: See the Machine the Agents Run On

## The claim

An agent can run a GUI app: an Electron window, a browser, an Android
emulator. Today nobody can see it. The agent works blind, and the person
cannot check what the agent did.

Helios gets one desktop for each host. A person opens it from the desktop app
or the phone, with the Helios login they already have. Agents draw on the same
desktop, so the person watches the agent's windows live.

When the machine has no desktop, the daemon installs one the first time
someone asks for it: xrdp, XFCE, and guacd.

**Scope:** `internal/desktop` (new), `internal/server`, `internal/terminal`,
`desktop/`, `mobile/`, and a release job that builds guacd. Linux hosts only in
this spec.

## Where we are

| Area | Today |
|---|---|
| A GUI app that an agent starts | Fails with "cannot open display", or runs under an Xvfb that nobody can see. |
| Checking a GUI change | The person must reach the machine some other way: VNC, an RDP client, or a cloud console. |
| Remote access to the host | Helios already has authenticated tunnels (spec 20, spec 30). |
| A live byte stream over a WebSocket | `terminal_ws.go` already relays an authenticated WebSocket to a local socket, without translating the bytes. |
| A typical cloud VM | Has no desktop, no X server, and often no user password (SSH keys only). |

On the reference machine (Ubuntu 26.04 LTS): xrdp, Xorg, XFCE and guacd are all
missing. apt has `xrdp` 0.10.1, `xorgxrdp` and FreeRDP 3. apt has **no**
`guacd`.

## The design in one picture

```
desktop app / phone                     helios daemon (Go)                       same host
guacamole-common-js ──WS (Helios auth)──▶ /api/desktop/tunnel ──TCP──▶ guacd 127.0.0.1:4822
                                                                         │ RDP
                                                                         ▼
                                                             xrdp 127.0.0.1:3389 ──▶ Xorg (xorgxrdp) + XFCE
                                                                                          ▲
                                                             agent sessions ── DISPLAY=:10 ┘
```

- **guacd** speaks RDP to xrdp and the Guacamole protocol to the client. The
  client is `guacamole-common-js`, which draws into a canvas.
- **The daemon replaces the Guacamole web app.** There is no Tomcat and no Java.
- **Only the daemon listens on the network.** xrdp and guacd bind to
  `127.0.0.1`.
- **One desktop for each host.** xrdp keeps the session when the viewer
  disconnects. The next viewer returns to the same desktop.

## Components

### D1. Auth: Helios auth only

The person never types an OS password. The bearer auth that protects the rest
of the API also protects the desktop.

The daemon still has to log in to xrdp. It does this with a secret that only it
holds:

1. At install, the daemon creates a random 32-byte secret in
   `~/.helios/desktop/secret`, with mode `0600`.
2. The install writes the SHA-256 of the secret to
   `/etc/helios/desktop/<user>.sha256`, owned by root, mode `0644`.
3. The install adds one line to `/etc/pam.d/xrdp-sesman`, above
   `@include common-auth`:

   ```
   auth sufficient pam_exec.so expose_authtok quiet /usr/lib/helios/desktop-auth
   ```

   `desktop-auth` is a short root-owned script. It hashes the password that PAM
   gives it on stdin and compares the hash with the user's file. A match passes.
   Anything else falls through to the normal password check, so a person who
   has a password can still log in to xrdp the usual way.
4. At connect time, the daemon puts the user name and the secret into the guacd
   handshake. The secret never leaves the daemon.

Only `xrdp-sesman` changes. SSH, sudo, and the console login do not change.

**Why a hash in `/etc`.** PAM runs as root and must not trust a file that the
user can change. The user's copy holds the secret. Root's copy holds only the
hash, so reading it gives an attacker nothing.

### D2. The tunnel

```
GET /api/desktop/tunnel?ticket=<t>&width=<px>&height=<px>&dpi=<n>
Sec-WebSocket-Protocol: guacamole
```

- **Ticket, not bearer.** A browser WebSocket and a WebView cannot set an
  `Authorization` header, and a JWT in a URL leaks into logs. So the client
  first calls `POST /api/desktop/ticket` with its bearer token. The answer is a
  ticket that works once, within 30 seconds.
- **The daemon does the handshake.** It opens TCP to guacd and sends `select`
  with the fixed protocol `rdp`. It reads `args`, then answers `size`, `audio`,
  `video`, `image` and `connect`. It fills every argument itself:
  - `hostname=127.0.0.1`, `port=3389`;
  - `username` and `password` from D1;
  - `width`, `height` and `dpi` from the query, within limits;
  - `resize-method=display-update`, `enable-wallpaper=false`,
    `color-depth=24`, `security=any`, `ignore-cert=true`.

  The client cannot choose a host or a port, so the tunnel cannot reach any
  other machine.
- **After the handshake, it relays.** It sends the tunnel-UUID instruction
  that `guacamole-common-js` expects, then copies frames both ways without
  translating them, the same way `terminal_ws.go` does.
- **Limits.** At most 4 viewers at a time for each host. Each viewer is one
  guacd connection to the same xrdp session.

### D3. The desktop runtime

`internal/desktop` owns the state of the desktop and reports it:

```
GET /api/desktop
```

```jsonc
{
  "state": "missing",          // missing | installing | starting | ready | failed | unsupported
  "missing": ["xrdp", "xfce", "guacd"],
  "can_install": true,         // `sudo -n true` works
  "install_command": "helios setup desktop",
  "display": ":10",            // when ready
  "viewers": 0,
  "error": ""                  // when failed, in words
}
```

- **guacd** runs as a child of the daemon, as the user, from
  `~/.helios/desktop/guacd/`: `sbin/guacd -f -b 127.0.0.1 -l 4822`. The daemon
  restarts it with back-off if it exits. If port 4822 is taken, it takes the
  next free port.
- **xrdp** is a system service from apt, enabled with systemd.
- **The xrdp session** starts the first time any guacd connection logs in. The
  daemon warms it right after install: it connects once and disconnects at
  once. xrdp keeps the session after a disconnect, because sesman runs with
  `KillDisconnected=false`. `Policy=UB` lets a viewer with a different screen
  size return to the same session.
- **The display number** comes from the session's Xorg process, which runs as
  the user with `-xrdp`. The daemon also finds the matching `XAUTHORITY` file.

### D4. Install on demand

The first open of a desktop that is `missing` starts the install. So does
`POST /api/desktop/install`. Each step reports progress through the SSE event
`desktop_state`, which carries the whole state: every step with its state and
its last output line.

| Step | What it does | Needs root |
|---|---|---|
| 1. Packages | `apt-get install -y --no-install-recommends xrdp xorgxrdp xfce4 xfce4-terminal dbus-x11` | yes |
| 2. xrdp config | Sets `port=tcp://127.0.0.1:3389` in `xrdp.ini`. Sets `KillDisconnected=false` and `Policy=UB` in `sesman.ini`. Adds the polkit rule that stops the "color profile" password prompts. | yes |
| 3. PAM | Writes `/usr/lib/helios/desktop-auth`, the hash file, and the line in `/etc/pam.d/xrdp-sesman` (D1). | yes |
| 4. Session | Writes `~/.xsession` with `startxfce4`, but only if that file does not exist. | no |
| 5. guacd | Downloads the guacd bundle (D5), checks its SHA-256, and unpacks it. | no |
| 6. Start | Enables and starts xrdp, starts guacd, and warms the session (D3). | yes, for xrdp |

- **Root steps run through `sudo -n`.** When `sudo -n true` fails, the daemon
  cannot escalate. It stops before step 1 with `can_install: false`, and the
  app shows the one command to run in a terminal: `helios setup desktop`. That
  CLI command runs the same steps with a normal sudo prompt.
- **Each step is idempotent.** A step that finds its work done passes at once.
  An install that failed halfway can run again.
- **The daemon never removes packages.** `helios setup desktop --uninstall`
  reverts only what Helios added: the PAM line, the helper, the hash file, the
  xrdp config keys, and the guacd bundle. It leaves the apt packages in place
  and says so.
- **Why this package set.** Base `xfce4` is the panel, the window manager, the
  file manager and the settings. It has no terminal app, so `xfce4-terminal`
  comes too: a person must be able to open a shell on the desktop.
  `xfce4-goodies` adds about 30 apps and panel plugins (battery, Wi-Fi,
  sensors, a disc burner) that suit a laptop, not a server watched remotely.
  `--no-install-recommends` keeps the install near 45 packages instead of
  about 175.
- **Other desktops.** When the host already has a desktop session for xrdp,
  for example GNOME, step 4 leaves `~/.xsession` alone. XFCE is installed only
  when no session exists.

### D5. The guacd bundle

Ubuntu no longer ships guacd, so Helios builds it.

- **The release workflow gains a job.** It builds `guacamole-server` 1.6.0 with
  FreeRDP 3, for `linux-amd64` and `linux-arm64`. It builds only the RDP
  protocol: no VNC, SSH or telnet support.
- **One folder, no system libraries.** The job copies every shared library
  that guacd needs, except glibc, into `lib/`. It sets the rpath to
  `$ORIGIN/../lib` with `patchelf`. The build runs on the oldest glibc that the
  daemon supports, so the bundle runs on newer distros too.
- **The asset** is `helios-guacd-1.6.0-linux-<arch>.tar.zst`, published on the
  same release as the daemon. The daemon binary embeds the expected SHA-256 of
  each asset and refuses a mismatch.
- **Updates.** A new guacd version means a new embedded hash. The daemon
  replaces the bundle on its next start.

### D6. Agents draw on the desktop

When the desktop is `ready`, each new agent session gets `DISPLAY` and
`XAUTHORITY` in its environment. These go through the env map that `SpawnHost`
already takes (`internal/terminal/spawn.go:23`).

- **Only new sessions.** A running process cannot change its environment.
  Sessions that started earlier keep their old environment.
- **A setting turns it off:** `desktop.agent_display` (default `true`).
- **No desktop, no change.** When the desktop is not `ready`, nothing is set,
  and agents behave as they do today.
- **The Helios skill** (`helios setup skill`) tells agents that a desktop
  exists, and that a person may be watching it.

### D7. The desktop app

- **A Desktop view for each host,** opened from the host menu and from a
  button in the session header. It is not one of the session panels, because
  the desktop belongs to the host, not to one session.
- **The client** is `guacamole-common-js` 1.6.0. The library is vendored from
  the guacamole-client release, because the npm package is still at 1.5.0.
- **Input.** Keyboard, mouse and wheel. Clipboard text syncs both ways while
  the view has focus.
- **Screen size and DPI.** A display menu in the view sets the mode, the
  aspect ratio, the size and the DPI (D9). A 1:1 toggle shows exact pixels.
- **States.** These follow `GET /api/desktop`:
  - `missing` with `can_install`: an Install button, then live progress.
  - `missing` without `can_install`: the command to run, with Copy.
  - `starting`: a spinner.
  - `failed`: the error in words, with Retry.
- **Pop out.** The view can open in its own window, so the desktop sits next to
  the session.

### D8. The phone

- **The view** is a WebView that loads `/desktop/`, a small page embedded in the
  daemon. The page uses the same JavaScript client.
- **Auth.** The app gets a ticket with its bearer token and passes it into the
  page through the JavaScript bridge. The page never holds the bearer token.
- **Touch.**
  - Tap is a left click.
  - Long press is a right click.
  - Two fingers drag to scroll.
  - Pinch zooms the view, not the remote screen.
  - A drag with one finger after a double tap moves the pointer with the
    button held.
- **Keyboard.** A button opens the phone keyboard. A key bar adds Esc, Tab,
  Ctrl, Alt, the arrows, and Ctrl+C / Ctrl+V.
- **Screen size and DPI.** A sheet sets the same choices as the desktop app
  (D9). The remote screen keeps a desktop size, for example 1280×800, because a
  phone-size remote screen would make XFCE unusable. The view starts at
  fit-to-width.

### D9. Resolution, aspect ratio and DPI

A person controls two separate things. guacd treats them separately, so the
controls do too.

**Resolution and aspect ratio: how many pixels the remote screen has.**

- **Modes:**
  - **Fit** (the default): the remote screen follows the view's size, so the
    aspect ratio is the view's own.
  - **Fixed aspect:** 16:9, 16:10, 4:3, 3:2 or 21:9, with a height of 720, 900,
    1080 or 1440, or with "fit height", which takes the height from the view.
  - **Custom:** an exact width × height.
- **Letterbox.** With a fixed aspect, the view centres the screen and fills the
  rest with the background.
- **Live.** A change sends a Guacamole `size` instruction over the open tunnel.
  guacd turns it into an RDP display update, and xorgxrdp resizes the X screen.
  The connection stays open.
- **The handshake fixes the scale at 1.** guacd 1.6 multiplies the size by
  `dpi ÷ client dpi` at connect. The daemon sends the same value for both, so
  the remote pixel count is exactly what the client asked for.
- **Limits.** Width 640–7680, height 480–4320. The daemon clamps every
  request.

**DPI: how big text and controls are inside the desktop.**

- **Presets:** 96 (100 %), 120 (125 %), 144 (150 %), 192 (200 %), or a custom
  value from 72 to 288.
- **Live.** `PUT /api/desktop/display` with `{"dpi": 144}`. The daemon runs
  `xfconf-query -c xsettings -p /Xft/DPI -s 144` inside the desktop session,
  with its `DISPLAY` and D-Bus address. XFCE and GTK apps redraw at once, with
  no reconnect.
- **Why not RDP's scale factor.** guacd 1.6 always sends
  `DesktopScaleFactor = 0` in a display update, so the RDP channel cannot carry
  DPI after connect.
- **The answer** of `GET /api/desktop` gains `"dpi"` (the current value).

**Where the choice lives.**

- The client keeps the mode, the aspect ratio, the size and the DPI for each
  host: desktop in localStorage, phone in preferences.
- The phone starts with 16:10 at 1280×800 and DPI 120. The desktop app starts
  with Fit and DPI 96.
- The DPI is also stored on the host, because it belongs to the desktop
  session. A second viewer sees the same text size.

## Security

- **Network exposure.** xrdp and guacd listen on `127.0.0.1` only. Other
  machines can reach the desktop only through the daemon's authenticated
  WebSocket, over the tunnels that Helios already uses.
- **Local users.** Another account on the same machine can reach
  `127.0.0.1:3389`. It still needs this user's password or the secret, and the
  secret is in a `0600` file.
- **The tunnel cannot be redirected.** The daemon fixes the target of every
  connection (D2).
- **Tickets** work once, within 30 seconds, and only for the tunnel.
- **Install** runs only fixed commands with fixed arguments. Nothing from a
  request reaches a shell.
- **Audit.** Each tunnel open and close goes to the daemon log, with the device
  name.

## Failure modes

| Failure | What the person sees |
|---|---|
| `sudo -n` fails | "Helios needs root once to install the desktop. Run: `helios setup desktop`", with Copy. |
| apt is locked by another install | The step waits up to 2 minutes, then fails with "Another install is running. Try again when it ends." |
| The guacd download fails or the hash does not match | "Could not get the desktop service", and Retry. The daemon never runs a bundle that fails the check. |
| xrdp does not start | The last lines of `journalctl -u xrdp`, and Retry. |
| The XFCE session exits at once | A broken `~/.xsession` is the usual cause. The error names the file. |
| The host is macOS or Windows | `state: unsupported`. The view says that remote desktop needs a Linux host. |

## Rollout

1. **D5.** The guacd bundle job in the release workflow, with a smoke test that
   starts guacd from the bundle on a clean container.
2. **D3, D1 and D4.** The runtime, the auth and the install, behind the
   setting `desktop.enabled` (default `false`) for one release.
3. **D2.** The tunnel.
4. **D7 and D9.** The desktop app view, with the display controls.
5. **D6.** Agent `DISPLAY`.
6. **D8.** The phone.
7. Turn `desktop.enabled` on by default.

## Testing

- **Unit:**
  - The handshake builder fills every guacd argument and ignores anything from
    the client except size and dpi.
  - Tickets expire and work only once.
  - Each install step passes at once when its work is already done.
- **Integration, in a clean Ubuntu 26.04 container with systemd:**
  - The full install from nothing.
  - A tunnel opens, a frame arrives, and a second viewer joins the same session.
  - A fresh agent session can run `xdpyinfo` against its `DISPLAY`.
- **Live e2e:**
  - The real desktop app opens the Desktop view against an isolated daemon,
    clicks in an XFCE window, and checks the pixels in a screenshot.
  - The phone does the same on the emulator.
- **Security:**
  - A ticket that was already used is refused.
  - A query that names another host or port is ignored.
  - xrdp and guacd do not listen on any address except `127.0.0.1`.

## What we are not doing

- **macOS and Windows hosts.** guacd can reach macOS Screen Sharing over VNC.
  That is a later spec.
- **Apache Guacamole's web app.** No Tomcat and no Java. The daemon does its
  job.
- **Docker for guacd.** It would put Docker on every host that wants a
  desktop. The bundle needs nothing.
- **Audio, file transfer and printing.** guacd supports them. They need their
  own UI.
- **A desktop for each session.** One desktop for each host is simpler, and it
  matches how a person thinks about a machine.
- **Recording.** guacd can record sessions. That raises privacy questions for
  its own spec.

## Decisions on the open questions

1. **The guacd port.** When 4822 is busy, the daemon takes the next free port.
   Only the daemon talks to guacd, so the number does not matter to anyone
   else.
2. **Agent DISPLAY.** `desktop.agent_display` starts as `true`.
3. **The XFCE package set.** Base `xfce4` plus `xfce4-terminal` (D4).
