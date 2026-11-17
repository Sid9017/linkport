# Tokyo public relay deployment — November 17, 2026

The operator deployed `linkport-relay-jp-1` on Vultr Tokyo: 1 vCPU, 1 GB RAM,
25 GB SSD, Ubuntu 24.04, IPv4 `207.148.110.58`. The selected base plan is $5/month.
The existing `jp-proxy-2` was not modified.

## Live configuration

- Relay URL: `https://207.148.110.58` on TCP 443.
- Nginx terminates normal, trusted IP TLS and proxies `/relay` WebSockets.
- The relay process listens only on `127.0.0.1:3340`; that port is not public.
- TCP 80 serves HTTP ACME challenges; other HTTP paths return 404.
- Dedicated `linkport-relay` system user, systemd startup and restart on failure.
- Limits: 16 relay TCP connections, 4 MiB/s receive rate per client, 512 MiB
  relay-process memory ceiling. These are configured limits, not a load benchmark.
- Signed directory version 1 is served at
  `https://207.148.110.58/relay-directory.json` and embedded in new clients.
- Nginx access logging is disabled in this deployment. Aggregate relay counters
  and service errors remain available locally to the operator.

The trusted certificate has an IP SAN for `207.148.110.58`, initially expires
November 24, 2026 at 00:22:11 UTC (08:22:11 Asia/Shanghai), and is renewed with
Certbot 5.10 using the short-lived certificate profile. The systemd timer checks
renewal every six hours with up to fifteen minutes of jitter. A renewal deploy
hook validates and reloads Nginx. `certbot renew --dry-run` succeeded.

The installed Linux amd64 relay binary SHA-256 is:

```text
6502edc3e29289f41e70b7fe6bcbfabec82a7695365c9d696463c53d2a8698e7
```

## Verified behavior and limitations

The deployed service passed `TestPublicRelayDeployment` from the VPS using two
new temporary identities, direct IP transports disabled, and ordinary certificate
verification. It checked HTTPS health, ping, the directory signature and version,
pairing, rejection of unpaired requests and reused invitations, exact binary
round trips in both directions, independent streams, and revocation.
Both test clients were on the VPS. This is not a two-geography NAT traversal test.

Independent Check-Host HTTP probes to the HTTPS health URL returned HTTP 200 from
Canada (Vancouver) and Vietnam (Ho Chi Minh City). Romania (Bucharest) timed out.
The local development machine's direct HTTPS acceptance test and browser request
were reset before an HTTP response; the cause has not been established. These
results do not establish reachability from every ISP, country, or desktop device.
No actual USB serial hardware was used in deployment acceptance.

Relay, Nginx, and the renewal timer were confirmed active. Listening sockets showed
Nginx on public 80/443, ordinary SSH on 22, and relay on loopback 3340. The temporary
SSH 443 listener and both temporary artifact tunnels were stopped. Certificate and
device private keys were not included in artifact transfers.

## Client update policy

Current clients pin the relay set to the bundled operator-signed directory;
legacy custom relay
and discovery configuration migrates to maintained defaults while preserving
identity and pairings. User-facing relay URL, discovery URL and public-listener
controls are removed. The reusable hosting module remains available to operator
code and the standalone command.

Desktop checks application releases after startup and every six hours. Available
application updates show an update badge. Automatic updates stage verified
application packages; restarting remains explicit. Current builds have no remote
directory source, ignore legacy directory caches and reject runtime directory
replacement. Settings show “Updated with the app”; node changes require a new
application build. Direct-only mode stays disabled.

The bundled directory's freshness date is February 14, 2027. Its signature is
still checked after that date, so an installed application retains its fixed
relay. Future remote-update builds must reject expired incoming updates. The
directory signing key stays offline; it was not uploaded to the VPS. Use
`tools/relay-catalog` to sign an updated bootstrap before rebuilding desktop.
Application release signing uses a separate key. The operator authorized configuring the application release key and matching
public key in `Sid9017/linkport` GitHub Actions encrypted repository secrets. Both
`LINKPORT_UPDATE_PRIVATE_KEY` and `LINKPORT_UPDATE_PUBLIC_KEY` were configured and
their presence verified on November 17, 2026. No release was published.

Deployment templates and commands are in [the VPS deployment guide](../deploy/relay/vultr/README.md).

## Local delivery verification

The final root race suite and `go vet ./...` passed. Desktop production tests with
`-race` passed separately; isolated Chromium fixtures passed with the bundled
Wails runtime. The managed-directory test also exercises a real relay actor
through a refusing loopback proxy: replacing relay candidates keeps the endpoint,
failed persistence removes newly inserted candidates, and a newer signed version
restores a previously parked candidate. No public service is contacted by this test.

A local macOS arm64 desktop archive was built from the uncommitted checkout and
signed locally using the separate offline application release key. Its signature
was independently verified against the embedded public key. The archive is not a
published release and was not installed over the user's existing desktop app.

```text
linkport-desktop_0.0.1_darwin_arm64.zip
SHA256 258945fc6605823331bb96550e51481e471018d70d165bfd52cadd69c9121e00
```

## Pairing follow-up — November 17, 2026

Invitations now expire after eight hours and remain single-use. Replacing an
invitation or exiting its process still invalidates it. Receivers tolerate
copy-induced whitespace and invisible separators without changing decoded
credentials, and distinguish missing prefixes, malformed encoding/content,
expiry, clock errors and connection failures. Both machines should use the new
build for eight-hour invitations: old receivers impose the previous lifetime.

The pinned endpoint omits interface addresses when its UDP socket is bound to
an unspecified address. Invitations and authenticated contact records now also
include bounded LAN candidates from active non-loopback, non-point-to-point
interfaces. This gives iroh a direct LAN path when STUN or the public relay is
unavailable. Public pkarr publication continues to use the separate relay-only
publisher. The desktop reports the observed relay connection and last error,
rather than treating an initialized endpoint as a connected relay.

The root race suite and `go vet ./...`, separate desktop production race tests,
and isolated Chromium fixtures passed after these changes. Explicit regressions
also passed for wildcard-bound endpoints pairing over real local UDP without a
relay, eight-hour expiry, and formatted invitations preserving single-use
authorization. These checks do not establish pairing between two physical
machines or public-network reachability.

The user subsequently reported successful invitation-based pairing between two
physical machines using the repair build. This is user-reported pairing
acceptance; the selected direct/relay path, serial byte transfer and hardware
behavior were not independently observed.

The supplied complete invitation passed the original application parser before
its expiry. A subsequent native UI inspection read `inkport1.` in the submitted
field, confirming a missing leading `l` in that field while the chat-supplied
original had the complete prefix. This does not establish which copying or
editing step lost the character. The rebuilt native app was checked to report
the missing prefix explicitly and display the eight-hour lifetime.

The development Mac still received connection resets after approximately five
seconds on Tokyo HTTPS, HTTP and SSH. Trusted HTTPS with TLS 1.2 had the same
outcome. Fresh Check-Host HTTPS health probes from Baku, Sofia and Shiraz each
returned HTTP 200 (request `587ee0d1kfa2`). Other HTTPS destinations were
reachable from the Mac. The rebuilt native app also exposed the actual relay
WebSocket handshake reset. No authenticated server-side
inspection was possible: SSH reset before authentication and the provider
console remained behind a browser security verification page. The origin of
the resets is unresolved; a successful Mac-to-Tokyo acceptance test is still
required. No relay deployment or system network configuration was changed
during this follow-up.

## Bundled relay policy — November 17, 2026

At the user's request, GitHub directory distribution and mesh-wide publication
are deferred. The default build uses only the embedded Tokyo relay and verifies
its existing operator signature. No directory request goes to Tokyo or GitHub;
legacy directory caches cannot override the bundled list. Existing identity,
pairings, eight-hour invitations and the direct-only option are retained.
Application release polling continues independently. This policy change does
not establish that the Mac-to-Tokyo connection resets have been resolved.

The root race suite, root `go vet`, separate desktop production race suite,
and isolated Chromium fixtures passed with the bundled policy. The macOS arm64
test build is in `dist/desktop-fixed-relay`; its ad-hoc bundle signature
verified successfully. The native app was opened and Settings displayed
“随应用更新 · v1” without a relay-update button. Existing paired-device
inventory remained visible; no hardware serial session was opened for this check.

```text
linkport-desktop_0.0.1_darwin_arm64.zip
SHA256 1a8823a9586e74a405623a8d40a20c51e7d9d6f6db8147b1dbca2a3b125f8e42
```
