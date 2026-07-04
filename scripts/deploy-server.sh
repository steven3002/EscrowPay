#!/usr/bin/env bash
# Build and (re)launch the EscrowPay backend as a container on this host.
#
# The image is a static Go binary (see backend/Dockerfile); the host Go
# toolchain is older than the module requires, so the build happens inside
# Docker. Secrets never enter the image — configuration is supplied at run time
# from backend/.env (Nomba credentials + beneficiaries) and backend/.env.deploy
# (database, app secrets, public URL, trusted origins). The deploy file is read
# last, so it wins on any overlapping key.
#
# The container listens only on loopback; a Caddy reverse proxy in front of it
# terminates TLS and forwards from the public sslip.io hostname.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
IMAGE="${IMAGE:-escrowpay-api:local}"
NAME="${NAME:-escrowpay}"
HOST_PORT="${HOST_PORT:-127.0.0.1:8080}"   # loopback only; Caddy fronts it
ENV_BASE="$ROOT/backend/.env"
ENV_DEPLOY="$ROOT/backend/.env.deploy"

for f in "$ENV_BASE" "$ENV_DEPLOY"; do
  [ -f "$f" ] || { echo "missing env file: $f" >&2; exit 1; }
done

echo "Building $IMAGE from backend/Dockerfile…"
docker build -t "$IMAGE" "$ROOT/backend"

echo "Replacing container $NAME…"
docker rm -f "$NAME" >/dev/null 2>&1 || true
docker run -d \
  --name "$NAME" \
  --restart unless-stopped \
  -p "$HOST_PORT:8080" \
  --env-file "$ENV_BASE" \
  --env-file "$ENV_DEPLOY" \
  "$IMAGE" >/dev/null

echo "Waiting for health…"
for i in $(seq 1 30); do
  if curl -fsS --max-time 3 "http://127.0.0.1:8080/healthz" >/dev/null 2>&1; then
    echo "Healthy: $(curl -fsS http://127.0.0.1:8080/healthz)"
    exit 0
  fi
  sleep 1
done
echo "Backend did not become healthy in time; recent logs:" >&2
docker logs --tail 40 "$NAME" >&2
exit 1
