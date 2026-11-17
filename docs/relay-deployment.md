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

Production clients use the operator-signed relay directory; legacy custom relay
and discovery configuration migrates to maintained defaults while preserving
identity and pairings. User-facing relay URL, discovery URL and public-listener
controls are removed. The reusable hosting module remains available to operator
code and the standalone command.

Desktop checks release and relay-directory updates after startup and every six
hours. An available application or directory update shows an update badge.
Automatic updates stage verified application packages and apply verified directory
updates; restarting an application remains explicit. Disabling automatic updates
keeps checking and manual apply actions available. Invalid signatures, expiry,
rollback and version conflicts cannot replace the installed directory. Offline
checks preserve installed configuration; direct-only mode stays disabled.

The directory expires February 14, 2027. The operator must publish a higher signed
version before then. The directory signing key stays offline; it was not uploaded
to the VPS. Use `tools/relay-catalog` to sign and atomically replace the served JSON.
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
