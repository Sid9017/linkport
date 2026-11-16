# Changelog

## 0.0.1 — development preview

This is an early framework and design review preview. Serial application
behavior and the desktop experience are still under development. This release
does not represent a completed serial application or a production desktop app.

- Establishes version metadata, native platform build checks, deterministic
  archive packaging, SHA-256 checksums, and a draft release workflow.
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
- Adds explicit Tailscale login actions in the UI and system tray. Browser
  authorization uses the installed official CLI; there is no automatic login.
- Replaces native desktop demo data with real Tailscale peer discovery, local
  serial enumeration and persistent sharing controls. The separate design
  preview keeps its illustrative fixtures.
- Publishes an authenticated Tailscale-only sharing service on TCP 7700 and
  adds `linkport serve --tailscale` for machines without the desktop shell.
- Adds RFC 2217 data transport, per-session PTY/com0com mappings, actual RX/TX
  records, serialized sends, and revocation on unshare or device removal.
- Adds native full-buffer log export with byte-preserving HEX and escaped text.
- Adds direct local serial monitoring without Tailscale or sharing, with the
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
