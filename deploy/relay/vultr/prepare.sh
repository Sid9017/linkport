#!/bin/bash
set -euo pipefail
cd -- "$(dirname -- "$0")"
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq nginx python3-venv ca-certificates curl
python3 -m venv /opt/linkport-certbot
/opt/linkport-certbot/bin/pip install --disable-pip-version-check 'certbot>=5.4,<6'
/opt/linkport-certbot/bin/certbot --version
id linkport-relay >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin linkport-relay
install -m 0755 linkport-relay /usr/local/bin/linkport-relay
install -m 0644 linkport-relay.service /etc/systemd/system/linkport-relay.service
install -d -m 0755 /var/www/linkport/.well-known/acme-challenge
# Optional compatibility file for desktop builds predating GitHub distribution.
if [ -f relay-directory.json ]; then
  install -m 0644 relay-directory.json /var/www/linkport/relay-directory.json
fi
install -m 0644 nginx-http.conf /etc/nginx/sites-available/linkport-http
ln -sfn /etc/nginx/sites-available/linkport-http /etc/nginx/sites-enabled/linkport-http
if [ -L /etc/nginx/sites-enabled/default ]; then unlink /etc/nginx/sites-enabled/default; fi
if [ -f /run/sshd-relay-bootstrap.pid ]; then kill "$(cat /run/sshd-relay-bootstrap.pid)"; fi
nginx -t
systemctl daemon-reload
systemctl enable --now linkport-relay nginx
systemctl reload nginx
ufw allow 80/tcp
ufw allow 443/tcp
curl --fail --silent http://127.0.0.1:3340/healthz
