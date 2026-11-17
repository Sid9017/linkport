# Linkport

**Use a serial port on another machine through a local virtual port.**

Linkport connects paired devices over iroh and makes explicitly shared serial
ports available to your existing tools. Run it on both machines, pair them with
a single-use invitation, and choose which ports to share. No account or separate
VPN client is required.

The desktop app includes a serial monitor, text and HEX sending, and independent
sessions for multiple ports. A headless CLI runs the sharing service on machines
without a desktop.

**Status:** 0.0.2 early release series with Stable and Nightly release channels. Windows, macOS, and Linux are target
platforms. Automated tests exercise encrypted loopback connections and isolated
serial fixtures; real hardware, drivers, and cross-network behavior still need
validation in their target environments.

## How it works

```text
Your serial tool                                               Remote hardware
       │                                                              │
Local virtual port ←→ Linkport A ←── encrypted iroh ──→ Linkport B ←→ Serial port
    PTY / com0com                  direct or relayed           explicitly shared
```

Each installation generates its own persistent device identity. Pairing verifies
the peer's public key and establishes mutual trust. Ports start unshared, and
pairing does not enable sharing automatically.

iroh handles connection establishment, hole punching, and path selection. It can
use a relay when a direct path is unavailable. Relays forward encrypted traffic;
serial access decisions remain on the two devices. Address discovery helps locate
a device, while its online status comes from an authenticated service probe.

## Run the desktop app

The CLI and desktop modules both pin **Go 1.26.11**. The desktop app uses
**Wails v3.1.0** and has a separate Go module under `desktop/`.

From a source checkout:

```sh
cd desktop
go run -tags production .
```

Native builds require the platform's development dependencies. Linux needs GTK 4
and WebKitGTK 6 development headers. macOS needs Xcode Command Line Tools;
packaged macOS applications target macOS 14.5 or later. See the
[desktop guide](docs/desktop.md) for build and packaging details.

To package the app for the current machine, run from the repository root:

```sh
python3 scripts/desktop.py
```

Output goes to `dist/desktop/`. On macOS, this includes `Linkport.app` with an
ad-hoc signature for local use; production signing and notarization are separate
release work.

### Connect two machines

1. Start Linkport on both machines.
2. On one machine, open **Add device** and generate a pairing invitation.
3. Transfer the complete invitation through a trusted channel and import it on
   the other machine. Invitations expire after eight hours and can be used once.
4. On the machine with the hardware, explicitly enable sharing for the desired
   serial port.
5. On the other machine, select the paired device, shared port, and serial
   settings, then connect.
6. Open the displayed local virtual port in your serial tool, or use Linkport's
   built-in monitor.

Each connection has its own mapping and log. Disconnecting one session leaves
other sessions running. A physical port has one owner at a time: local monitoring
and remote sharing cannot control it simultaneously.

Paired devices exchange signed contact records and keep a local cache. If a saved
address stops working, Linkport can ask another paired device for the target's
new contact information. Discovery records never grant trust or serial access.

The desktop can also host the same relay implementation as the standalone service.
Hosting is opt-in under device network settings, with TLS required for public
listeners. See the [relay and discovery guide](docs/relay.md) for configuration,
resource limits, multi-relay recovery and the current implementation boundaries.

The monitor can also open a local serial port directly, without pairing,
networking, or a virtual-port driver. Pausing freezes the display; closing a
session releases its port. Removing a peer, stopping sharing, or unplugging a
port closes the affected sessions. Reconnecting a terminated session is currently
manual.

## Run a headless sharing service

Run these commands from the repository root. Replace the example port path with
an actual path or identity reported by `ports`; use the actual `COMx` on Windows.

```sh
go run ./cmd/linkport ports
go run ./cmd/linkport ports --json
go run ./cmd/linkport share /dev/cu.usbserial-example
```

Pair before starting the long-running service:

```sh
# Machine A: print an invitation and stay online while B imports it.
go run ./cmd/linkport invite

# Machine B: import A's complete invitation.
go run ./cmd/linkport pair 'linkport1.COMPLETE_INVITATION'

# Machine A: stop the invitation process with Ctrl-C, then start sharing.
go run ./cmd/linkport serve
```

Useful management commands:

```sh
go run ./cmd/linkport identity
go run ./cmd/linkport devices
go run ./cmd/linkport forget DEVICE_ID
go run ./cmd/linkport unshare /dev/cu.usbserial-example
```

Device management commands require exclusive access to the device configuration.
Exit the desktop or sharing process first, or use the running desktop app to
manage devices. CLI sharing changes are picked up by the running sharing service.
Flags precede positional arguments; use `--directory DIR` on device management
commands or `--config FILE` on sharing commands when selecting other configuration
locations. See [device and network configuration](docs/iroh.md) for details.

## Networking and privacy

The default configuration uses Linkport-maintained HTTPS relays and public address discovery,
alongside LAN discovery. Public address records contain relay addresses, not
serial inventories or logs. LAN discovery supplies contact information and does
not grant access.

Clients support two network modes:

| Mode | Relay behavior | Address discovery |
| --- | --- | --- |
| `default` | Bundled, operator-signed Tokyo relay | Paired-device exchange, public lookup, and LAN discovery |
| `disabled` | No relay | No public discovery; direct addresses and LAN discovery |

Public relay addresses cannot be edited by end users. This build pins the relay
set to the bundled signed directory containing only `https://207.148.110.58`
in Tokyo. It verifies the embedded operator signature, ignores legacy directory
caches and does not fetch a remote relay list. Node additions and removals
require an application update for now. Legacy custom configuration migrates
to the maintained defaults without replacing device identity or pairings.
Peer advertisements cannot add arbitrary outbound relay candidates.
See [directory maintenance](docs/relay-directory.md) for the current policy.

```sh
go run ./cmd/linkport network --relay-mode default
go run ./cmd/linkport network --relay-mode disabled
```

Desktop checks for application updates, displays an update badge, and automatically
downloads signed application updates when background updates are enabled.
Application installation still requires an explicit restart. Turning automatic
updates off keeps application checks and manual actions available. Relay settings
show “Updated with the app”; there is no separate relay-list check or apply action.

The relay module remains reusable by operator code and the standalone command;
ordinary desktop users cannot configure a public relay listener. Deployment and
validation evidence is recorded in [the Tokyo deployment report](docs/relay-deployment.md).
Saving the network mode pauses the old transport and requires an app restart;
independent local monitoring continues to work.

Device identity, trusted peers, and network settings are stored in
`os.UserConfigDir()/linkport/devices.json`. **This file contains the device's
private key. Keep it private and do not copy it between devices.** Unix files use
`0600` permissions; Windows inherits the configuration directory's access controls.
Sharing choices are stored separately in `linkport/shares.json`.

Removing a peer revokes its local access and disconnects its streams. Resetting
identity generates a new key and clears the local trust list; other machines
must remove the old identity and pair again. A local reset cannot revoke a key
from another machine's trust list.

Relays cannot decrypt serial content, but their operators can observe connection
metadata and relayed traffic volume. Metadata retention depends on the operator.
The project uses the pinned community implementation
`github.com/tmc/go-iroh v0.3.0`, which is not the official n0 Go SDK.

## Platform support and limits

| Platform | Local virtual port | Requirements and limits |
| --- | --- | --- |
| macOS / Linux | PTY | Created automatically; no physical DTR/RTS passthrough. Parameter polling may miss rapid intermediate changes. |
| Windows | com0com pair | Install a compatible driver and configure port pairs beforehand. Each simultaneous session needs a separate pair. The driver is not bundled. |

On Windows, enter the application end and Linkport end of an existing com0com
pair when connecting. An arm64 application build does not establish arm64 driver
compatibility. On macOS/Linux, Linkport creates per-session links under
`~/.linkport/dev/` and removes them on disconnect.

The data channel uses RFC 2217 inside authenticated, encrypted iroh streams. It
preserves binary bytes and handles supported serial settings and control updates.
Hardware flow control and sustained BREAK are not implemented. Physical modem
signals remain subject to the adapter and virtual-port capabilities.

Ports with reliable USB serial numbers can be identified across replugging.
Otherwise, identity falls back to the device path; replacement hardware at the
same path may not be distinguishable. Ambiguous duplicate identities require
local confirmation.

Loopback tests do not establish universal NAT traversal, Rust iroh interoperability,
USB hardware compatibility, or public-relay availability. See
[CI and compatibility testing](docs/ci.md) for the exact verification scope.

## Development

Run the backend checks from the repository root and the desktop check from its
own module:

```sh
go test -race -count=1 -timeout=5m ./...
go vet ./...

cd desktop
go test -tags production -count=1 -timeout=5m ./...
```

Root-module checks do not exercise the desktop module. Browser integration uses
isolated fixtures; setup and commands are in [docs/ci.md](docs/ci.md).

To view the desktop design with demo devices and traffic:

```sh
go run ./cmd/linkport design
```

Open `http://127.0.0.1:7710`. This preview does not access real serial hardware.
The native desktop app uses actual devices and sessions. The separate
`serve --local-api` mode exposes a loopback, read-only development API; it does
not expose the real remote serial data channel.

Routine CI runs **five runner jobs**: three native backend/desktop jobs for
Windows, Linux, and macOS, plus Linux and Windows frontend jobs. Full builds for
all six platform/architecture combinations run on manual full checks or releases.
Actual com0com driver tests require a dedicated runner and manual dispatch.

Build CLI archives with `python3 scripts/release.py`; validate the packaging
script with `python3 scripts/release.py --self-test`. Desktop packages use
`scripts/desktop.py`. Release automation publishes complete signed Stable releases and Nightly prereleases
after full checks. Unpublished local archives carry a commit identity and never update themselves.
Public downloads and channel-specific update manifests are served from
`https://linkport.grezl.link`; the source repository can remain private. See the
[release guide](docs/releasing.md).

## Documentation

| Topic | Guide |
| --- | --- |
| Product scope | [PRODUCT.md](PRODUCT.md) |
| Documentation index | [docs/README.md](docs/README.md) |
| Devices, relays, discovery, and data ownership | [System architecture](docs/system-architecture.md) |
| Pairing, connections, and revocation flows | [Interaction sequences](docs/interaction-sequences.md) |
| Identity and network configuration | [iroh guide](docs/iroh.md) |
| Serial adapters and runtime modules | [Runtime architecture](docs/architecture.md) |
| Desktop, monitoring, and updates | [Desktop guide](docs/desktop.md) |
| Tests and platform verification | [CI guide](docs/ci.md) |
| Builds, signing, and releases | [Release guide](docs/releasing.md) |

Some detailed documents are currently written in Chinese.
