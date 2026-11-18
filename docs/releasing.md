# Versioning, packages and publication

`VERSION` contains the next base semantic version. `internal/version.Version`
defaults to that base followed by `-dev`, and `website/package.json` carries the
same base. Use the shared command instead of editing these values separately:

```sh
python3 scripts/version.py patch
# Also supports minor and major. Review the new CHANGELOG section, then commit.
```

## Public release channels

| Channel | Version and tag | Distribution and updates |
| --- | --- | --- |
| Stable | `0.0.5`, tag `v0.0.5` | Published GitHub release; only newer Stable updates |
| Nightly | `0.0.5-nightly.YYYYMMDDHHMMSS.RUN`, matching `v` tag | Prerelease; only newer Nightly updates |

Unpublished local/CI archives use `0.0.5-dev.EPOCH.gCOMMIT[.dirty]` metadata,
appear as "Local build" in the desktop UI, and never check for updates. They are
not a third public download or update channel. A plain `go build` reports the
base `-dev` version with unknown commit/date.
Packagers inject `Version`, `Channel`, `Commit` and `Date` in
`github.com/Sid9017/linkport/internal/version`. Tagged packages require a clean
checkout and a tag resolving to HEAD. Existing tags and published assets are
not overwritten. Changes after publication require another version.

Nightly runs daily at 18:00 UTC (02:00 Asia/Shanghai the following day), or on
manual dispatch. It uses `main`, skips an unchanged commit, creates an immutable
tag and calls the same release workflow. Routine PR/main compatibility checks
still use five jobs. Website and version-tool tests share the existing Linux
backend job. Releases use the full six-target native profile.

## Local builds

Use the pinned Go toolchain and Python 3.11 or later. macOS requires the native
SDK and cgo; Linux desktop requires GTK 4 and WebKitGTK 6. These commands create
unpublished local packages by default:

```sh
python3 scripts/release.py --validate-only
python3 scripts/release.py --self-test
python3 scripts/release.py --output dist/cli-dev
python3 scripts/desktop.py --output dist/desktop-dev
```

Names identify both program and complete version:
`linkport-desktop_VERSION_GOOS_GOARCH.zip` for desktop;
`linkport_VERSION_GOOS_GOARCH.tar.gz` for Unix CLI or `.zip` for Windows CLI.
Desktop ZIPs contain exactly one top-level payload: `Linkport.app` on macOS,
`Linkport.exe` on Windows, or `linkport-desktop` on Linux. This is required by
the Wails updater. Version/channel metadata and the bundled CLI remain embedded
in the executable; the font license remains embedded with the font assets.
CLI-only CI archives also include `VERSION`, `CHANNEL`, documentation and
dependency licenses.
The native executable retains its normal name. macOS bundle numeric versions
use the base version; the app, updater and archive show the complete version.

`--target` asserts the native host; it does not cross compile. macOS/Linux/Windows
amd64 and arm64 packages are built on their target native runners. Archive
ordering, permissions, timestamps and ownership are normalized. Reproducibility
requires the same compilers, SDKs and compression libraries. Packages retain the
existing app identity/configuration, so replacing a channel preserves pairings.
Do not run two channels simultaneously against the same configuration.

## Stable release

After reviewing changes and passing checks, commit the release sources, then:

```sh
git tag v0.0.5
git push origin v0.0.5
```

The tag event runs `release.yml`. An existing reviewed tag can also be dispatched:

```sh
gh workflow run release.yml -f tag=v0.0.5
```

The workflow validates the version and tag, runs full root/desktop/frontend
checks and builds the native CLI/desktop CI artifacts. Publication validates the
six desktop archive names and writes SHA-256 digests and their Ed25519
signatures into `desktop-checksums.txt`. Both
`LINKPORT_UPDATE_PRIVATE_KEY` and `LINKPORT_UPDATE_PUBLIC_KEY` are mandatory;
a missing or mismatched signing key fails publication. Private keys reach only
the final signing job, never platform build jobs or the website.

A draft is assembled first and published only after every archive and the
signed checksum metadata have uploaded. Stable becomes the latest release; Nightly is a
prerelease and never replaces Stable as latest. Notes come from the current
base version's CHANGELOG section. A failed partial draft stays unpublished;
inspect it before retrying. No installer is offered from a CI artifact page.

These are early 0.0.x releases. macOS bundles have ad-hoc signing, without Apple
notarization. Windows Authenticode/com0com driver verification and physical
USB/cross-network tests are distinct from package signatures and native CI.

## Public downloads with private source

The existing `Sid9017/linkport` source and Release storage remain private.
Netlify is configured to serve the website and release gateway at
`linkport.grezl.link` once deployment and runtime credentials are set up:

- `/api/releases`: published Stable/Nightly versions and desktop/CLI downloads.
- `/updates/stable.json` and `/updates/nightly.json`: Wails endpoint manifests.
- `/downloads/TAG/FILENAME`: allowlisted package, checksum or signature assets.

Configure `LINKPORT_GITHUB_TOKEN` in the Netlify project's runtime environment:
use a dedicated token restricted to this repository with read-only Contents
permission. It is server-only and must never be placed in source or client
bundles. Link the existing repository to Netlify with publish directory
`website/dist`; `netlify.toml` supplies build, functions and route configuration.
An authorized Netlify GitHub App connection for builds does not by itself
configure the function's separate runtime credential.

The gateway ignores drafts and unpublished local tags, checks all six desktop checksums and
signatures before advertising a release, and exposes only explicitly named
package assets. GitHub download credentials never reach a client; authenticated
asset requests yield temporary CDN redirects without forwarding the token.
Unconfigured or failed backends return 503, and an unpublished channel returns
204. The website shows unavailable/unpublished states rather than fabricated
links. No repository visibility change or new repository is required.

Desktop pins the publisher key and uses the same public manifests, checks the
channel and semantic version, verifies the signed digest before offering an
update, then verifies downloaded bytes before staging. Restart remains explicit.
Disabling automatic downloads keeps version checks active. Unpublished local builds never
replace themselves. Test a real upgrade separately from provider/fixture tests.
Users of 0.0.1 must manually install 0.0.2 or later once to switch to the new
public update source.

## Desktop-only release assets

New published releases upload the six `linkport-desktop_*.zip` archives and
`desktop-checksums.txt`, with no standalone `.sig` files. Each checksum line
is followed by `# ed25519 BASE64_SIGNATURE FILENAME`; the signature authenticates
the raw SHA-256 digest. The public gateway verifies these inline signatures and
continues to support older releases with sidecar signatures. Standalone CLI
archives remain available as full-CI build artifacts, but are not published as Release assets.
The desktop build embeds the corresponding console CLI; its first normal launch
registers the user command as described in [desktop.md](desktop.md#bundled-command-line-tool).
Update signatures remain required by the public download service and updater.
Existing published releases are not modified by this workflow change.
