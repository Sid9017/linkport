# Linkport desktop 0.0.1

The desktop host uses Wails v3.1.0 with Go 1.26.11. It lives in a separate
module under `desktop/`, reusing the root module through a local replacement.
The root CLI remains on Go 1.23.4.

```sh
cd desktop
go run -tags production .
```

Package a native executable or macOS application from the repository root:

```sh
python3 scripts/desktop.py
```

Use `--go /absolute/path/to/go` when choosing a toolchain explicitly.
macOS output is `dist/desktop/Linkport.app`, with a native title bar, menu bar
icon, single-instance handling and an ad-hoc signature for local use.
The default close action hides the window; the tray menu or Dock restores it.
Quit exits the process. The preference can change close into quit.
The application icon shows two white serial sockets joined by a curved cable
on black, with a matching transparent menu bar template.
The window has a fixed sidebar and no outer page scrolling. Long device lists
scroll inside their content area; the monitor keeps its command bar visible
while logs scroll independently.

Windows output is `Linkport.exe`; Linux output is `linkport-desktop`.
The application sets its runtime window/tray icon. Installers and production
code signing are separate release work. Linux native builds require GTK 4
and WebKitGTK 6 development headers. macOS packaging targets macOS 14.5+.

## Settings and Tailscale

The top-right gear controls text size, Chinese/English language, monochrome
theme, close-to-tray behaviour and background update preference. The native
application saves validated settings to the user's config directory at
`linkport/desktop.json`; the browser preview uses its own local storage.
The native HTTP bridge requires an unpredictable per-launch token.
The native document explicitly imports `/wails/runtime.js` as an ES module.
This initializes title bar mouse dragging; CSS drag regions alone are not
sufficient. The brand passes pointer events to the title bar, while settings
and network buttons remain excluded from dragging.

Tailscale is installed and logged in separately. Login actions appear in the
header/settings and native tray menu. They run the installed official
`tailscale login --timeout=90s` only after the user selects the action.
For an already authenticated but stopped network, the same action runs
`tailscale up` without preference flags; it does not reset Tailscale settings.
Only complete HTTPS login URLs on the official Tailscale authentication
hosts are opened; credentials and auth URLs are not written to logs.
If Tailscale is absent, the action opens its official download page.
The application reads local Tailscale status and never silently invokes login.

The native application reads real local Tailscale status and peers, enumerates
real serial devices, and persists explicit sharing choices. It never initializes
the separate design preview's demo sessions. Local inventory is refreshed about
once a second; peer/service discovery runs again five seconds after each pass.
Unavailable status queries retain established sessions and mark cached network
status as stale; confirmed logout, network shutdown, or an offline peer closes
affected sessions. There is no automatic data-session reconnection yet.

Each machine publishes the opt-in serial list and data service on its Tailscale
IPs only, TCP 7700, and authenticates callers using the installed CLI's WhoIs.
Tailscale access rules must permit that port. Remote peers must run Linkport
desktop, or `linkport serve --tailscale`; machines without a reachable service
are visible but have no fabricated serial list.

Connections use an HTTP upgrade followed by RFC 2217 binary transport with
framing-setting acknowledgments. Each active connection has its own PTY or
com0com pair, receive/transmit logs, bounded buffers and serialized writes.
Windows users enter their already configured com0com pair when connecting.
PTY does not carry DTR/RTS. Hardware flow control and sustained BREAK are not
implemented. Stopping sharing or unplugging the device revokes its session.

The monitor preserves byte data for exact HEX display. Text commands use UTF-8
with the selected line ending. Export uses the native Save dialog and writes
the complete retained session buffer, including records hidden by filtering,
with timestamps, HEX and escaped text. Logs are bounded to 500 records/128 KiB
per session. Physical device behavior and a real two-machine tailnet still need
verification in the target environment; loopback bridge tests use real PTYs and
an isolated simulated physical serial endpoint.

The monitor's serial-port button opens actual local devices directly, without
Tailscale or a sharing selection. Local sessions support the same timestamped
RX/TX, sending, pause, HEX, clearing and native export. Closing a local session
releases the physical device. Network shutdown does not close local sessions;
device removal does. A shared physical-port broker prevents local monitoring
and incoming remote sessions from opening the same device concurrently.

## Background updates

Updates use the Wails updater with a signed GitHub provider. Linkport checks
after 30 seconds and then every six hours when the preference is enabled.
New desktop archives are downloaded, checked against SHA-256 and Ed25519
signatures, and staged. The user chooses restart; Wails' helper performs
replacement and recovery. The application does not overwrite its running
binary directly or restart in the middle of a debugging session.

0.0.x builds allow published pre-releases and ignore drafts. The provider
matches `linkport-desktop_VERSION_GOOS_GOARCH.zip`, never CLI archives.
It expects `desktop-checksums.txt` and a base64 `ARCHIVE.zip.sig` sidecar.
The signature covers the raw SHA-256 digest, matching Wails' Ed25519 verifier.

No signing key is generated or installed by building the app. Without an
embedded public key, automatic installation is disabled and Settings reports
that release signing has not been configured.

Generate a signing key deliberately, in a secure path of your choosing:

```sh
go run ./tools/update-sign -generate /secure/path/linkport-update.key
```

The command refuses to overwrite an existing key, creates the private file
with mode 0600, and prints only the base64 public key. Never commit the private
key. Configure the release workflow secrets:

- `LINKPORT_UPDATE_PRIVATE_KEY`: the private file's base64 contents.
- `LINKPORT_UPDATE_PUBLIC_KEY`: the public key printed by the tool.

The desktop build embeds the public key. The release job signs each desktop
archive and rejects a mismatched key pair. Both values are required together;
the private key is not passed to platform build jobs or embedded in the app.
A macOS production release should also use Developer ID signing and notarization.

The default feed is `Sid9017/linkport`. `LINKPORT_UPDATE_REPOSITORY=owner/repo`
can select a public releases repository at build time, useful when the source
repository is private. The CI builds default to the source repository; changing
the release destination also requires updating the release publishing workflow.
No GitHub token is shipped in the app.
