# Linkport desktop 0.0.1

The desktop host uses Wails v3.1.0 with Go 1.26.11. It lives in a separate
module under `desktop/`, reusing the root module through a local replacement.
The root CLI uses the same Go 1.26.11 toolchain.

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

## Settings and device pairing

The top-right gear controls text size, Chinese/English language, monochrome
theme, close-to-tray behaviour and background update preference. The native
application saves validated settings to the user's config directory at
`linkport/desktop.json`; the browser preview uses its own local storage.
The native HTTP bridge requires an unpredictable per-launch token.
The native document explicitly imports `/wails/runtime.js` as an ES module.
This initializes title bar mouse dragging; CSS drag regions alone are not
sufficient. The brand passes pointer events to the title bar, while settings
and network buttons remain excluded from dragging.

The header, settings and tray menu open device management. There is no account
login or external VPN dependency. Each installation generates an independent
persistent identity in `linkport/devices.json`; the same file contains trusted
public keys and relay configuration. Unix permissions are 0600; Windows uses
the user configuration directory ACL. Never share this file with another device.

Create a single-use five-minute invitation on one machine and paste it on the
other. Both machines must be running. The encrypted handshake pins the public
keys; the invitation authorizes mutual trust, not automatic sharing of ports.
Removing a device disconnects its streams and denies subsequent requests.
Identity reset requires entering the full current device ID, clears local trust,
and pauses networking until restart. Other devices must remove the old key and
pair again; local reset cannot revoke a stolen key from their trust lists.

The application probes only paired devices, enumerates real serial devices,
and persists explicit sharing choices. Public relay/discovery, custom HTTPS
services and direct-only networking are available in the device dialog. Saving
network settings immediately pauses old transports and takes effect on restart.
Direct-only mode does not publish to public discovery or connect to public
relays. Temporary peer probe failures do not terminate healthy serial streams.
The local inventory is refreshed about once a second; peer probes repeat five
seconds after each pass. Data-session reconnection remains manual.

`linkport serve` runs the same iroh service without the desktop shell.
`linkport serve --local-api` exposes only the loopback read-only development API.
See [iroh.md](iroh.md) for CLI pairing, privacy and deployment boundaries.

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
per session. Physical device behavior and real two-machine iroh connectivity still need
verification in the target environment; loopback bridge tests use real PTYs and
an isolated simulated physical serial endpoint.

The monitor's serial-port button opens actual local devices directly, without
networking or a sharing selection. Local sessions support the same timestamped
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
