# Changelog

## Unreleased

- Separate the title bar's device-pairing icon from relay status. Show a green
  dot only for a connected relay, a gray dot otherwise, and the deduplicated
  count from the latest network probe plus the connected home relay.
- Show an arrow-and-text update button for available, downloading, ready and
  retry states, with download progress and English/Chinese labels.
- Avoid repeatedly announcing unchanged relay status during polling.

## 0.0.7 — native desktop packages and reliable updates

- Ship macOS DMG, Windows EXE and Debian/Ubuntu DEB desktop packages for both architectures, with the CLI embedded and no ZIP or standalone signature assets.
- Select and verify updates for the running OS and architecture; prepare macOS bundles from verified DMGs and hand Linux package installation to the system installer.
- Migrate from 0.0.6 or earlier with one manual installation of this release.

- Keep update checks bounded to thirty seconds while allowing downloads up to
  thirty minutes, with cancellation and streaming verification preserved.
- Show check/download timeouts, verification, staging and restart failures
  separately in Chinese and English, and display download percentages.
- Derive failure state from the completed operation so delayed error events
  cannot overwrite a retry; clear previous errors and progress before retrying.

## 0.0.6 — compatible desktop update packages

- Package exactly one application payload per Desktop ZIP: Linkport.app on
  macOS, Linkport.exe on Windows, or linkport-desktop on Linux. Remove top-level
  documentation and metadata so the Wails updater can extract the application.
- Retain the embedded CLI, version/channel metadata and font license inside the
  application; upgrades carry the matching command-line tool.
- Keep publisher signatures inline in desktop-checksums.txt. Release assets
  contain six Desktop ZIPs and one checksum file, with no standalone .sig or CLI
  downloads. The public gateway supports both inline and legacy signatures.
- Add English/Chinese website switching with English as the first-visit default
  and a saved language preference across the homepage and documentation.
- Preserve the current version's changes in generated release notes.
- Verify the archive layout for all six targets locally and confirm a real macOS
  archive reaches the Wails updater's ready state in isolated staging.


## 0.0.5 — desktop CLI and update delivery

- Embed the matching console CLI in Desktop and register a per-user `linkport`
  command on first launch, without administrator access. Desktop upgrades carry
  the corresponding CLI; existing commands and saved settings are preserved.
- Publish Desktop archives only, with their update signatures and checksums;
  standalone CLI archives remain full-CI artifacts rather than Release assets.
- Check Stable/Nightly updates every minute after the initial thirty-second delay.
- Follow the system UI language on first launch: Chinese uses Simplified Chinese;
  English and unsupported languages use English. Saved choices take precedence.
- Validate CLI registration and upgrades in isolated user directories. Windows
  environment registration and real serial hardware still need target-environment
  verification; hosted native build checks do not certify drivers or hardware.
- Wait for modal close completion before clicking the underlying device button,
  and for a visible close control before checking focus styles. Keep reduced-motion
  keyboard assertions, bounded browser deadlines and stage diagnostics.

## 0.0.4 — validation tag only

Publication was blocked by a modal-transition fixture timing failure. No release
assets were published; the desktop delivery changes are included in 0.0.5.

## 0.0.3 — validation tag only

Publication was blocked by a WebKit fixture timing failure. No release assets
were published; the desktop delivery changes are included in 0.0.5.

## 0.0.2 — release channels and pairing fixes

- Keep the Tokyo relay bundled with the application, retain its operator signature,
  and disable independent relay-directory checks and legacy cache overrides.
- Extend single-use invitations to eight hours, accept copied whitespace and invisible
  separators, and report prefix, encoding, expiry and connection errors separately.
- Include bounded LAN interface candidates for wildcard-bound iroh endpoints.
- Show actual relay connectivity and the last error. User-reported two-machine
  invitation pairing succeeded; public-network and hardware limits remain documented.
- Add Stable and Nightly as the two public release channels. Unpublished local
  builds carry commit metadata and do not update themselves. Stable and Nightly
  only use their own channel.
- Publish complete signed CLI and desktop packages for macOS, Windows and Linux on
  amd64 and arm64 after the full compatibility checks. Every desktop update must
  pass SHA-256 and the embedded publisher's Ed25519 verification.
- Provide website downloads, public installation instructions, generated version
  notes and an anonymous update endpoint, independently of private source access.
- Preserve the device identity, pairings and explicit application restart on upgrades.
- macOS packages remain unnotarized. Real USB serial, cross-network NAT and Windows
  com0com driver behavior still require verification in their target environments.

## 0.0.1 — development preview

This is an early framework and design review preview. Serial application
behavior and the desktop experience are still under development. This release
does not represent a completed serial application or a production desktop app.

- Establishes version metadata, native platform build checks, deterministic
  archive packaging, SHA-256 checksums, and a draft release workflow.
- Keeps independent Windows, Linux and macOS CI with five routine runner jobs,
  merged backend/virtual-port checks, and separate frontend tests. Runs the full
  architecture matrix and packaging manually or for releases, cancels stale PR
  runs, and limits artifact retention. Real com0com testing is manual and fails
  when required driver configuration is missing.
- Adds opt-in local serial sharing rules, USB identity matching, polling and
  hotplug snapshots, bounded subscriptions, and a read-only development SSE API.
- Provides PTY and com0com virtual-port backends behind one Go interface.
- Adds an interactive desktop design review with persistent left/right areas,
  curved animated connections, dashed local/remote-port frames, bundled Geist
  fonts and custom SVG icons. The preview uses demo data.
- Provides CLI commands for version, local ports, sharing choices, snapshots,
  the loopback development API, and the design preview.
- Targets macOS, Linux, and Windows on amd64 and arm64 for the `cmd/linkport`
  command. Platform builds do not certify physical serial hardware or drivers.
- Adds a native Wails v3 host, macOS menu bar template icon, single-instance
  behaviour and close-to-tray handling. A macOS `.app` is packaged alongside
  desktop archives.
- Adds a flat socket-and-cable graphic logo, grayscale themes, settings for text size,
  Chinese/English language, appearance and background updates.
- Uses embedded iroh networking with persistent per-device identities, single-use
  pairing invitations, trusted-device management, and immediate peer revocation.
  No external VPN client or account login is required.
- Supports public/custom relays and discovery, or direct-only networking; pauses
  old transports immediately when network settings or identity are changed.
- Runs authenticated sharing with `linkport serve`, and retains the loopback
  development API through `linkport serve --local-api`.
- Uses real paired-device discovery, local serial enumeration and persistent
  sharing controls in the native desktop; the design preview keeps fixtures.
- Adds RFC 2217 data transport, per-session PTY/com0com mappings, actual RX/TX
  records, serialized sends, and revocation on unshare or device removal.
- Adds native full-buffer log export with byte-preserving HEX and escaped text.
- Adds direct local serial monitoring without networking or sharing, with the
  same RX/TX, send, pause, HEX, clear, export and close actions. A physical-port
  broker arbitrates local monitoring against incoming remote sessions.
- Fixes native title bar dragging by loading the Wails runtime explicitly and
  preserving non-draggable interactive controls.
- Tests binary traffic and serial-setting propagation through real local PTYs
  and an isolated simulated physical endpoint. Actual two-machine and physical
  device behavior remain environment-specific verification work.
- Adds background GitHub update checks, signed artifact staging and a user
  restart action through Wails. Automatic installation is disabled until a
  release verification key is embedded and matching signed releases exist.
- Windows virtual ports require a separately installed, compatible com0com
  driver and an existing port pair. Drivers are not bundled. In particular,
  producing a Windows arm64 executable does not establish com0com arm64 support.
- macOS detailed serial enumeration requires cgo and Apple's IOKit. macOS
  binaries are built on native Macs; archives are unsigned and not notarized.
