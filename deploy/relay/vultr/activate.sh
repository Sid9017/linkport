#!/bin/bash
set -euo pipefail
cd -- "$(dirname -- "$0")"
test -r /etc/letsencrypt/live/linkport-relay/fullchain.pem
install -m 0644 nginx-https.conf /etc/nginx/sites-available/linkport-https
ln -sfn /etc/nginx/sites-available/linkport-https /etc/nginx/sites-enabled/linkport-https
nginx -t
systemctl reload nginx
install -d -m 0755 /etc/letsencrypt/renewal-hooks/deploy
printf '#!/bin/sh\n/usr/sbin/nginx -t && /bin/systemctl reload nginx\n' > /etc/letsencrypt/renewal-hooks/deploy/linkport-nginx
chmod 0755 /etc/letsencrypt/renewal-hooks/deploy/linkport-nginx
install -m 0644 linkport-certbot.service /etc/systemd/system/linkport-certbot.service
install -m 0644 linkport-certbot.timer /etc/systemd/system/linkport-certbot.timer
systemctl daemon-reload
systemctl enable --now linkport-certbot.timer
curl --fail --silent https://207.148.110.58/healthz
