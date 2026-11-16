# Agent instructions

<!-- magpie:begin · written by magpie from its Library -->
<Lark>
Use lark skill to report progress to current user if possible. The message should start with the session name to distinguish with each other. If there is local artifact to share, use tailscale IP. Create a chat group for each thread.
</Lark>
<!-- magpie:end -->

## Project context

- Linkport is a Go/Wails application for explicit serial sharing between paired
  devices. Device identity and transport use embedded iroh, with no account login.
- Read `PRODUCT.md` for the product boundary, `docs/system-architecture.md` for
  endpoint/service responsibilities, and `docs/interaction-sequences.md` for flows.
- The root module and `desktop/` are separate Go modules. Both use the toolchain
  pinned in their `go.mod`; root-only checks do not exercise the desktop module.
- `github.com/tmc/go-iroh` is a pinned community implementation. Check lifecycle,
  protocol compatibility and platform behaviour when changing its version.
- Preserve the distinction between implemented features, verified behaviour and
  planned deployment. Browser fixtures and cross-compilation do not establish
  real hardware or public-network compatibility.

## Code Review Rules

Report findings in Chinese. For each issue, identify its concrete trigger,
impact and the changed code responsible. Focus on actionable regressions in
security, byte integrity, resource ownership and platform behaviour.

### Device identity and authorization

- Authenticate peers using the public key verified by the QUIC connection.
  HTTP headers, source addresses, discovery results and self-reported IDs must
  not substitute for that identity.
- Pairing requires a valid unexpired single-use invitation, and the submitted
  identity must match the handshake. Unpaired peers must not read inventories
  or open serial streams while an invitation is active.
- Private seeds must not enter browser APIs, logs, discovery records, archives,
  screenshots or version control. Read failures must not silently replace an
  existing identity.
- Removing a peer or resetting identity must close relevant live connections,
  deny subsequent requests, and prevent cached connections or in-flight pairing
  from restoring revoked access. Local reset does not revoke remote trust lists.

### Stream and serial ownership

- Both ends must accept streams on an established iroh connection regardless of
  which end dialed it. Closing one stream must not close unrelated sessions.
- Remote serial access requires current pairing, explicit local sharing,
  unambiguous device identity and exclusive physical-port ownership.
- Local monitoring and incoming sharing must use the same physical-port broker.
  Cancellation, unshare, unplug, failed setup and revocation must release their
  streams, handles, mappings and workers.
- Preserve raw bytes, RFC 2217 escaping and actual setting acknowledgements.
  Keep control updates and data writes ordered; bounded buffers must fail
  visibly rather than silently discard data or grow without limit.

### Local UI and platform boundaries

- Native management routes require the per-launch token. The loopback
  development API must not become an unauthenticated remote data bridge.
- A transient discovery failure must not tear down a healthy data session.
  Network reconfiguration must leave independent local monitoring usable.
- Review root and desktop impacts together. Preserve platform build tags,
  PTY limitations, independent Windows com0com pairs and the distinction between
  driverless tests and actual driver verification.
- Treat names and remote metadata as untrusted UI text. Fixture-only behaviour
  must not become the native application's data source.

Do not report documented pre-existing limitations as newly introduced bugs
without a concrete regression caused by the PR. The project does not yet claim
automatic serial-session reconnection, universal NAT traversal or production
hardware verification.

## Relevant validation

- Root: `go test -race -count=1 -timeout=5m ./...` and `go vet ./...`.
- Desktop: from `desktop/`, `go test -tags production -count=1 -timeout=5m ./...`.
- Browser integration: follow `docs/ci.md`; run isolated fixtures instead of
  opening a user's hardware ports or relying on a public relay.
- Hardware/driver checks require their target environment. Do not describe an
  unavailable check as passed or silently replace it with compilation.
