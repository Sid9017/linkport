# Changelog

## 0.0.1 — development preview

This is an early framework and design review preview. Serial application
behavior and the desktop experience are still under development. This release
does not represent a completed serial application or a production desktop app.

- Establishes version metadata, native platform build checks, deterministic
  archive packaging, SHA-256 checksums, and a draft release workflow.
- Separates Windows, Linux and macOS CI into backend, virtual-port and frontend
  jobs; adds Chromium/WebKit fixture tests and a dedicated opt-in com0com driver
  workflow that fails when required driver configuration is missing.
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
