# Maintaining the bundled relay directory

The current application pins its relay set to the signed directory in
`internal/relaycatalog/bootstrap.json`. It contains one Tokyo relay:
`https://207.148.110.58` on TCP 443. Node additions and removals require an
application update for now.

`internal/relaycatalog/catalog.go` deliberately leaves `SourceURL` empty.
Desktop does not poll GitHub or the relay VPS for a directory, ignores legacy
directory caches, and rejects runtime directory replacement. Settings show
“Updated with the app” without a separate check or apply button. Application
release checks continue independently.

The embedded Ed25519 operator signature is still verified at startup. Relay
authorization is separate from device-signed contact records: a peer's ticket
or announcement cannot add an arbitrary relay. Direct-only mode remains
available and does not enable public relay or public address discovery.

## Files and responsibilities

| File | Responsibility |
| --- | --- |
| `internal/relaycatalog/bootstrap.json` | Signed relay set shipped with the application; currently the only source |
| `deploy/relay-directory.json` | Matching public signed deployment artifact; legacy clients may still fetch it |
| User configuration directory's `linkport/relay-directory.json` | Legacy cache ignored by the current build; left intact |

The VPS handles the relay protocol, health checks and certificate challenges.
Its legacy static directory route is optional for current clients. GitHub
directory publication and mesh-wide directory propagation are deferred.

## Updating the bundled list

1. Verify new relay endpoints separately and prepare the complete desired list.
2. Choose a directory version higher than all previously distributed versions.
3. Sign with the existing offline directory signing key. It is separate from
   the application release signing key.
4. Verify the signed result locally and copy the same bytes into the bootstrap.
5. Test both Go modules, build desktop and distribute the new application.

From the application source checkout:

```sh
go run ./tools/relay-catalog \
  -key /secure/path/relay-directory.key \
  -version 2 \
  -relays https://207.148.110.58 \
  -output deploy/relay-directory.json

go run ./tools/relay-catalog -verify deploy/relay-directory.json
cp deploy/relay-directory.json internal/relaycatalog/bootstrap.json
```

Replace version `2` with the next unused version. For node additions or
removals, pass the complete comma-separated target list to `-relays`. Editing
signed URLs, version or expiry without signing again invalidates the signature.
Keep the private key outside the repository, desktop archives and relay VPS.

The signer creates an 89-day validity period. The embedded list remains
authenticated after that period so an installed application does not lose
its configured relay because a directory server is unavailable. Future
runtime directory updates, if enabled, must pass freshness and monotonic
version checks as well as signature verification.

## Validation and retained update implementation

Root tests verify that a newer signed legacy cache and a damaged legacy cache
cannot change the bundled relay, runtime application is rejected, and an
invalid embedded signature fails startup. Desktop tests verify that starting,
manually checking and fetching directory updates issue no HTTP requests in
bundled mode. Browser fixtures verify the “Updated with the app” state.

The signed remote-update implementation is retained for a future publisher
configuration. Its tests use isolated HTTP fixtures with an explicit source.
It is not active in this build. A nonempty source must not be shipped until
its anonymous distribution, signatures, expiry and version handling are
verified. No GitHub credentials belong in desktop builds.
