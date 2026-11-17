# Compatibility testing

Pull requests and pushes to `main` start three independent workflows:
**Windows**, **Linux**, and **macOS**. Feature-branch pushes do not start a second
copy of the PR checks. New PR commits cancel superseded runs for the same PR;
manual full checks and release runs are not canceled this way.

## Routine and full profiles

Routine checks start **five runner jobs**: three native backend/desktop jobs,
plus separate Linux and Windows frontend jobs. Each native job runs the root
suite once, including virtual ports, and tests the desktop module without
packaging it. The routine targets are Windows amd64, Linux amd64, and macOS arm64.

Full checks run both architectures on every platform, all three frontend
environments, and CLI/desktop archive builds for all six targets. They start
**15 runner jobs** instead of repeating virtual-port packages in separate jobs.
The release workflow explicitly selects this profile. Intel and Apple Silicon macOS
downloads are both retained.

| Layer | Windows | Linux | macOS |
| --- | --- | --- | --- |
| Routine native target | amd64 | amd64 | arm64 |
| Full native targets | amd64 and arm64 | amd64 and arm64 | amd64 and arm64 |
| Backend and virtual port | Root suite includes adapter/decoder tests; driver suite compiled | Root suite includes actual PTYs, bridge and local monitor; race detector | Root suite includes actual PTYs, bridge and local monitor; race detector |
| Frontend | Chromium on Windows amd64 | Chromium and WebKit on Linux amd64 | Chromium and WebKit on Apple silicon |
| Frontend schedule | Routine and full | Routine and full | Full only |
| Desktop | Routine module tests; full packaging on both architectures | Routine module tests; full packaging on both architectures | Routine module tests; full packaging on both architectures |
| com0com kernel driver | Separate manual dedicated-runner workflow | Not applicable | Not applicable |

`windows.yml`, `linux.yml`, and `macos.yml` call `platform.yml` to keep shared
steps consistent. Native and frontend jobs remain independent. In the full
profile both must pass before `desktop.yml` tests and builds that platform's
two desktop archives. The draft release waits for all three platforms and their
packages. Reusable calls pass the profile explicitly; they do not infer it from
the caller's event name.

After these workflows are available on the default branch, manually run all
six targets without creating a release:

```sh
gh workflow run ci.yml --ref main
```

Or build only one platform's two architectures:

```sh
gh workflow run macos.yml --ref main -f full=true
```

Use the desired branch in `--ref`. Setting `full=false` on a platform workflow
runs just its routine checks.

Go dependencies and build outputs are cached using both modules' lockfiles.
Routine reports are uploaded only on failure; full runs retain their reports
for three days. Downloadable archives are retained for seven days. These
changes reduce future compute/storage use; they do not restore already consumed
included usage.

## Backend and PTY coverage

The backend job runs the root module's full test suite, covering explicit
sharing, persistence, peer identity, RFC 2217 framing and acknowledgements,
session isolation, device removal and revocation. It also validates and
packages the native CLI in the full profile. Windows-specific tests cover
fallback replacement on filesystems without POSIX rename support, preserving
the previous file when replacement fails, and Unicode/long paths.

The Unix backend job runs the whole root suite with `-race`, including
`internal/vport`, `internal/session`, `internal/engine` and `internal/p2p`.
These tests create actual operating-system
PTYs and isolated simulated physical endpoints. Coverage includes binary
round trips, parameter forwarding, reopening, independent mappings, blocked
operation cancellation, local monitoring and physical-port ownership.
They do not require USB hardware, an account or a public relay. The iroh suite uses real encrypted loopback endpoints to verify pairing, invitation expiry/replay, peer revocation and stream cancellation. The engine suite carries binary data through iroh and real PTYs, with physical modem outputs omitted for the simulated UART.

## Frontend coverage

The checked-in browser suite is `scripts/ui/check.cjs`, with a pinned Playwright
dependency and lockfile. CI invokes it through the desktop Go test harness
using `LINKPORT_UI_FIXTURES=1`. That mode replaces machine enumeration and
public network discovery with isolated providers, and browser API fixtures drive
connections without opening serial devices or signing into a network.

The browser loads the real desktop HTML, live JavaScript and bundled Wails
runtime. Tests check title-bar drag messages and button exclusion, Windows COM
pair fields, connection errors/success, sharing, local monitoring, closing and
isolating sessions, exact HEX bytes, millisecond timestamps, sending, log search,
pause/clear/export and viewport overflow/empty-state layout. Search and reading
positions are checked while retained records are evicted, including updates
while the monitor is hidden and eviction during an active search animation.

These are browser interaction tests, not native WebView2/WKWebView automation.
They verify that the runtime receives a drag request, not that the OS physically
moves the native window. USB hardware, native dialogs, driver installation and
real two-machine iroh access still need target-environment testing.

Run locally after installing the desktop Go toolchain and native dependencies:

```sh
cd scripts/ui
npm ci --ignore-scripts
npx playwright install chromium webkit
cd ../../desktop
LINKPORT_UI_FIXTURES=1 \
LINKPORT_UI_CHECK="$PWD/../scripts/ui/check.cjs" \
LINKPORT_UI_BROWSERS="chromium webkit" \
go test -tags production -count=1 -v -timeout=5m -run '^TestLiveDesktopBrowser$' .
```

`LINKPORT_CHROMIUM_PATH` optionally selects an installed Chrome executable.
`LINKPORT_UI_OUTPUT` selects the screenshots, traces and JSON report directory;
the default is `dist/ci/ui`. CI uploads these as `frontend-report-PLATFORM` on
failure or during full validation. Backend JSON logs include virtual-port
results; routine desktop test logs are included in the same native report.
Only fixture data is captured by the frontend workflow.

## Real com0com driver coverage

The hosted Windows workflow does **not** claim to test an installed com0com
kernel driver. It executes decoder/adapter tests and compiles the driver
integration suite for each selected native target. Full validation covers
both Windows architectures.

`com0com.yml` provides a separate **Windows com0com driver** workflow. It runs
only when explicitly dispatched. It is not a default release gate and a green
hosted Windows run does not mean the real driver has passed. A dedicated runner
must be configured before dispatching it.

To enable it:

1. Register a dedicated Windows x64 self-hosted runner with the additional
   `linkport-com0com` label.
2. Install a compatible com0com driver and reserve a port pair exclusively for
   these tests. Keep the default null-modem pinout and settings notifications
   enabled; leave baud-rate emulation disabled. Do not use a production hardware port.
3. Set repository variables `LINKPORT_TEST_COM0COM_PATH` to the application end
   (for example `COM121`) and `LINKPORT_TEST_COM0COM_PAIR` to the Linkport end
   (for example `COM122`).
4. Dispatch the workflow after it is available in the default branch. Routine
   pushes and PR updates do not queue tests against the reserved pair.

The job serializes access to the reserved pair and runs:

```powershell
$env:LINKPORT_TEST_COM0COM_REQUIRED = "1"
go test -tags com0com_integration -count=1 -v -timeout=2m `
  -run '^TestCom0comDriver' ./internal/vport
```

Missing endpoint configuration, missing/incompatible drivers, transfer failures
or missing settings/control-line notifications fail the job. They are not
converted to successful skips. Tests check binary transfer including `0x8e`
and `0xff`, baud-rate notifications, DTR/RTS, CTS/DSR, cancellation of an
overlapped read and releasing/reopening the pair.

The workflow neither installs a kernel driver automatically nor changes
Windows boot/signature settings. Windows arm64 driver compatibility remains
unverified until there is a compatible driver and an appropriate runner;
building the arm64 application is a separate result.

References:

- [Playwright CI setup](https://playwright.dev/docs/ci)
- [com0com source and driver installation requirements](https://github.com/datamancer/com0com/blob/master/com0com/ReadMe.txt)
