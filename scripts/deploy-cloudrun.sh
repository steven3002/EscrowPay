#!/usr/bin/env bash
# Deploy the EscrowPay backend to Cloud Run.
#
# Secrets never enter the image. This merges backend/.env (Nomba creds +
# beneficiaries) with backend/.env.deploy (database, app secrets, public URL,
# trusted origins) — the deploy file wins on conflicts — and passes the result
# to Cloud Run as environment variables. An intentionally-empty value (e.g.
# NOMBA_SIGNATURE_KEY) is dropped, leaving that feature off.
set -euo pipefail

export PATH="$HOME/google-cloud-sdk/bin:$PATH"

SERVICE="${SERVICE:-escrowpay-api}"
REGION="${REGION:-us-east1}"          # close to Neon's AWS us-east-1
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
ENV_BASE="$ROOT/backend/.env"
ENV_DEPLOY="$ROOT/backend/.env.deploy"

command -v gcloud >/dev/null || { echo "gcloud not found on PATH"; exit 1; }
PROJECT="$(gcloud config get-value project 2>/dev/null || true)"
[ -n "$PROJECT" ] || { echo "No project set. Run: gcloud config set project <PROJECT_ID>"; exit 1; }
echo "Project=$PROJECT  Service=$SERVICE  Region=$REGION"

echo "Enabling APIs (run, cloudbuild, artifactregistry)…"
gcloud services enable run.googleapis.com cloudbuild.googleapis.com artifactregistry.googleapis.com

# Merge env files → comma-separated KEY=VALUE, skipping comments/blank/empty and
# stripping one layer of surrounding double quotes. Values contain no commas.
ENVVARS="$(awk '
  /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
  {
    line=$0
    sub(/^[[:space:]]*export[[:space:]]+/, "", line)
    eq=index(line,"=")
    if (eq==0) next
    key=substr(line,1,eq-1); val=substr(line,eq+1)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", key)
    if (substr(val,1,1)=="\"" && substr(val,length(val),1)=="\"") val=substr(val,2,length(val)-2)
    env[key]=val
  }
  END { for (k in env) if (env[k] != "") { printf "%s%s=%s", sep, k, env[k]; sep="," } }
' "$ENV_BASE" "$ENV_DEPLOY")"

echo "Environment keys being set:"
echo "$ENVVARS" | tr ',' '\n' | sed -E 's/=.*/=•••/' | sort | sed 's/^/  /'

echo "Deploying (Cloud Build from backend/Dockerfile; a few minutes)…"
gcloud run deploy "$SERVICE" \
  --source "$ROOT/backend" \
  --region "$REGION" \
  --platform managed \
  --allow-unauthenticated \
  --min-instances=1 \
  --no-cpu-throttling \
  --port=8080 \
  --set-env-vars "$ENVVARS"

URL="$(gcloud run services describe "$SERVICE" --region "$REGION" --format='value(status.url)')"
echo
echo "Backend live:  $URL"
echo "Health check:  $URL/healthz"
echo
echo "Next: set Vercel BACKEND_ORIGIN=$URL, and for live funding set the Nomba"
echo "dashboard webhook to $URL/api/webhooks/nomba and put its key in backend/.env.deploy."
