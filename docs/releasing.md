# Releasing the framework preview

v0.0.1 is an early framework and design review preview, not a completed serial
application or a production desktop release. The archives contain the
`cmd/linkport` executable, `VERSION`, `CHANGELOG.md`, `README.md`, and the Geist
font license. The design preview and font assets are embedded in the command
and available with `linkport design`. Desktop designs and prototypes are review
material. A separate Wails build now packages a native macOS `.app` and
Windows/Linux desktop executables. Native desktops use real Tailscale/serial
providers and a real data bridge; only the separate design preview uses fixtures.
Validate physical devices, two-machine networking, and platform drivers in the
target environment before treating the development release as production-ready.

## Version and metadata

Keep `VERSION` and the default `internal/version.Version` at `0.0.1` for this
preview. A release tag must be exactly `v` followed by `VERSION`. Validation also
requires the existing local tag to resolve to HEAD and a clean checkout.
Plain `go build` defaults to version `0.0.1`, commit `unknown`, and date `unknown`.
The command can display `version.String()` or the individual exported fields.

The packager injects these Go string variables with linker `-X`:

```text
github.com/Sid9017/linkport/internal/version.Version
github.com/Sid9017/linkport/internal/version.Commit
github.com/Sid9017/linkport/internal/version.Date
```

`Commit` is the full HEAD hash, with `-dirty` appended for local modified sources.
`Date` is UTC RFC3339, derived from the commit timestamp, or `SOURCE_DATE_EPOCH`
when explicitly supplied. Tagged builds should leave `SOURCE_DATE_EPOCH` unset
to use the same commit timestamp on every runner.

## Local build and checks

Run from the repository root with Git, the Go toolchain specified by `go.mod`,
and Python 3.11 or later. macOS also needs Xcode Command Line Tools and its SDK.
The script uses only the Python standard library and sets
`GOCACHE=/tmp/linkport-gocache` for Go builds (an absolute native equivalent,
such as `C:\tmp\linkport-gocache`, on Windows).

```sh
export GOCACHE=/tmp/linkport-gocache
python3 scripts/release.py --validate-only
python3 scripts/release.py --self-test
go test -count=1 -timeout=5m ./...
python3 scripts/release.py
```

On Windows, use `python` if `python3` is unavailable, and set
`$env:GOCACHE = 'C:\tmp\linkport-gocache'` in PowerShell before running Go tests.

The packager builds `./cmd/linkport` for the native host only and produces one
archive plus `dist/checksums.txt`. `--target darwin_arm64`, for example, asserts
the host platform; it does not cross compile. Use a fresh output directory per
version; unrelated archives are rejected. `dist/` is ignored by Git.

Names are `linkport_0.0.1_<GOOS>_<GOARCH>.tar.gz`, or `.zip` on Windows.
Each archive contains a root-level executable (`linkport.exe` on Windows).
Tar owners, modes, ordering, gzip headers, and ZIP timestamps/modes are
normalized; the binary uses `-trimpath`, `-buildvcs=false`, and a blank build ID.
Repeatability assumes identical source, metadata, Go/Python/zlib versions,
native compiler, SDK, and build environment. Hosted runner image revisions are
not frozen, so byte identity across runner upgrades is not guaranteed.
Local modified sources carry the `-dirty` suffix and are not release inputs.

To inject metadata directly, for example on a native Mac:

```sh
GOCACHE=/tmp/linkport-gocache CGO_ENABLED=1 go build -mod=readonly -trimpath -buildvcs=false \
  -ldflags="-s -w -buildid= -X github.com/Sid9017/linkport/internal/version.Version=0.0.1 -X github.com/Sid9017/linkport/internal/version.Commit=$(git rev-parse HEAD) -X github.com/Sid9017/linkport/internal/version.Date=$(git show -s --format=%cI HEAD)" \
  -o /tmp/linkport ./cmd/linkport
```

## CI and native platforms

Every push and pull request runs separate Windows, Linux and macOS workflows.
Each separates backend, virtual-port and frontend jobs, then builds both native
desktop architectures after those checks pass. The release workflow calls
`ci.yml`, which reuses all three platform workflows with the validated commit.
Only the final draft job gets `contents: write`. Coverage and driver-runner
configuration are documented in [ci.md](ci.md).

| GOOS | GOARCH | GitHub runner | cgo |
| --- | --- | --- | --- |
| darwin | arm64 | `macos-15` | enabled |
| darwin | amd64 | `macos-15-intel` | enabled |
| linux | amd64 | `ubuntu-24.04` | disabled |
| linux | arm64 | `ubuntu-24.04-arm` | disabled |
| windows | amd64 | `windows-2022` | disabled |
| windows | arm64 | `windows-11-arm` | disabled |

Runner labels checked against GitHub's primary documentation on November 16,
2026:

- https://docs.github.com/en/actions/reference/runners/github-hosted-runners
- https://github.com/actions/runner-images
- https://github.com/actions/runner-images/issues/13518

GitHub retired macOS 14 runners on November 2, 2026. `macos-15` is the native
arm64 replacement; `macos-15-intel` builds amd64. Native macOS builds keep cgo
enabled for detailed serial enumeration through IOKit. Do not replace these
with Linux cross builds or disable cgo to make a build pass.

Windows virtual ports require a compatible, preinstalled com0com driver and an
existing port pair. The driver and its installer are not bundled. Hosted CI
tests the Windows adapter and decoder without installing a driver. The separate
`com0com.yml` workflow can test a real pair on a configured dedicated Windows
x64 runner; it is skipped until enabled and is not a default release gate.
Windows arm64 driver availability remains unverified. CLI macOS archives are
unsigned; desktop apps have an ad-hoc signature. Neither is notarized. Hardware
and native desktop usability need separate validation.

## Draft release procedure

Each platform workflow calls `desktop.yml`, building with Go 1.26.11.
Desktop artifacts live under `dist/desktop` and use ZIP on each platform.
`desktop-checksums.txt` lists only the six desktop ZIP filenames, matching the
updater provider. With both update signing secrets configured, the release job
adds an Ed25519 `.sig` file for each ZIP. Without keys it creates a draft
without enabling automatic installation. See `docs/desktop.md` before
configuring keys or enabling public updates.

No command below creates or pushes a tag. A maintainer must have separately
reviewed the design/status notes, committed the release sources, and made the
existing `v0.0.1` tag available on GitHub. `gh --verify-tag` prevents accidental
tag creation. No workflow publishes the draft.

For an existing tag, the tag push event triggers `release.yml`. Alternatively,
dispatch it after the reviewed workflow is on the default branch:

```sh
gh workflow run release.yml -f tag=v0.0.1
```

This is a GitHub write: run it only when ready to create a draft. Dispatch
requires the workflow on the default branch. The workflow checks out the tag,
validates its version and commit, runs the six-platform tests/builds, checks
that exactly six expected archives exist, generates `checksums.txt`, verifies
those hashes, and invokes `gh release create --draft --prerelease` with all
seven assets and `CHANGELOG.md` as the preview notes. An existing release causes
creation to fail; inspect it before deciding how to retry.

To collect native archives manually, copy all six into a clean ignored `dist/`
directory in the clean tagged checkout, then run:

```sh
python3 scripts/release.py --checksums-only --require-all --tag v0.0.1
(cd dist && shasum -a 256 -c checksums.txt)
gh release create v0.0.1 \
  dist/linkport_0.0.1_darwin_amd64.tar.gz \
  dist/linkport_0.0.1_darwin_arm64.tar.gz \
  dist/linkport_0.0.1_linux_amd64.tar.gz \
  dist/linkport_0.0.1_linux_arm64.tar.gz \
  dist/linkport_0.0.1_windows_amd64.zip \
  dist/linkport_0.0.1_windows_arm64.zip \
  dist/checksums.txt \
  --verify-tag --draft --prerelease \
  --title "linkport v0.0.1 — framework preview" \
  --notes-file CHANGELOG.md
```

Use `sha256sum --check checksums.txt` on Linux, or `Get-FileHash -Algorithm SHA256`
to compare archive digests on Windows. Checksums detect changed downloads; they
are not signatures. Review the draft assets and notes before any later,
separately authorized publication.
