# Public-IP relay on a 1 GB Ubuntu VPS

These files prepare a dedicated Ubuntu 24.04 instance for the operator-maintained
Linkport relay. Nginx owns public TCP 443; the relay accepts plaintext only on
`127.0.0.1:3340`. TCP 80 serves ACME challenges and otherwise returns 404.
The relay runs as a dedicated system user with 16 admitted TCP connections,
a 4 MiB/s receive limit per client, and a 512 MiB systemd memory ceiling.

Build `cmd/linkport-relay` for Linux amd64, place the binary as `linkport-relay`
next to these files, and copy the signed `deploy/relay-directory.json` alongside
it. Transfer this directory to the VPS, verify its archive SHA-256, and run
`bash prepare.sh` as root. The script installs Nginx and an isolated Certbot
virtual environment, starts the loopback relay, and enables the HTTP challenge
listener. It does not request a certificate or accept subscriber terms.

After the operator accepts the certificate issuer's current subscriber agreement,
request the production IP certificate interactively:

```sh
/opt/linkport-certbot/bin/certbot certonly \
  --webroot --webroot-path /var/www/linkport \
  --preferred-profile shortlived \
  --ip-address 207.148.110.58 --cert-name linkport-relay \
  --register-unsafely-without-email
```

The last option creates the ACME account without a contact email. It does not
disable TLS verification. Certbot must be at least 5.4 for IP webroot validation.
Run `bash activate.sh` after issuance. It enables HTTPS and a systemd timer that
checks renewal every six hours. A successful renewal tests and reloads Nginx.
This deployment does not automatically renew the signed relay directory; the
operator must publish a newly signed version before its expiry.

Verify issuance, automated renewal, and the actual relay protocol:

```sh
/opt/linkport-certbot/bin/certbot renew --dry-run
systemctl status linkport-relay nginx linkport-certbot.timer
ss -lntp
```

From a separate machine in this checkout:

```sh
LINKPORT_TEST_PUBLIC_RELAY=https://207.148.110.58 \
  go test -count=1 -v -timeout=2m ./internal/p2p -run '^TestPublicRelayDeployment$'
```

That explicit public check verifies trusted HTTPS and the signed directory, then
uses two temporary identities with direct IP transports disabled. It exercises
pairing, rejection of unpaired requests and invitation replay, binary round trips
in both directions, independent streams, and revocation. It does not open serial
hardware or prove connectivity from every NAT or geographic network.

Deployment status must be recorded from actual server checks. Merely preparing
these files or embedding the IP in a directory does not establish a live service.
