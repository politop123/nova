# NOVA deployment

This document describes the production deployment for the Go API, Go worker, Nuxt web app, pgvector/PostgreSQL, Redis, and Caddy on one Oracle Cloud VM.

## Delivery path

`dev` push → GitHub Actions verification → multi-architecture images in GHCR → SSH to Oracle → `/opt/nova/deploy-prod.sh <sha>` → schema initialization and Compose restart → Caddy → API/web.

The production files are `infrastructure/docker/docker-compose.prod.yml`, `infrastructure/caddy/Caddyfile`, and the scripts in `infrastructure/ops/`.

The repository is hosted at `politop123/nova`, and pushes to `dev` trigger the deployment pipeline.

## GitHub Actions secrets

Required repository or environment secrets:

- `ORACLE_HOST` — public IP or DNS name of the existing VM.
- `ORACLE_USER` — SSH user with Docker access.
- `ORACLE_SSH_KEY` — private key value for that VM.

Optional:

- `ORACLE_PORT` — SSH port, default `22`.
- `GHCR_USERNAME` and `GHCR_READ_TOKEN` — optional long-lived package credentials. When they are absent, the deployment job uses its short-lived GitHub token for images belonging to this repository.

Do not commit `.env`, private keys, tokens, or database passwords. The workflow updates only `GHCR_OWNER` and `IMAGE_TAG` in the VM's existing `/opt/nova/.env`.

## Oracle VM preparation

For the Oracle Linux image used by the Always Free VM, copy and run the idempotent bootstrap script:

```sh
scp infrastructure/ops/bootstrap-oracle-linux.sh opc@SERVER_IP:/tmp/
ssh opc@SERVER_IP 'chmod 700 /tmp/bootstrap-oracle-linux.sh && /tmp/bootstrap-oracle-linux.sh opc'
```

Reconnect over SSH after the script finishes so the `docker` group membership takes effect.

Allow inbound TCP `22` and `80` in the Oracle security list and VM firewall. Add `443` only when Caddy terminates TLS directly. Do not publish PostgreSQL `5432` or Redis `6379`.

Create `/opt/nova/.env` with mode `600`:

```dotenv
GHCR_OWNER=your-github-owner
IMAGE_TAG=dev
NOVA_USER_ID=replace-with-your-user-id
POSTGRES_DB=nova
POSTGRES_USER=nova
POSTGRES_PASSWORD=replace-with-a-long-random-password
OPENAI_API_KEY=
NOVA_TIMEZONE=Europe/Kyiv
DAILY_COST_BUDGET_USD=2
MONTHLY_COST_BUDGET_USD=30
WEB_ORIGIN=http://SERVER_IP
DOMAIN=:80
HEALTH_URL=http://127.0.0.1/health
```

The workflow preserves the other values in this file. Keep all provider and database secrets only on the VM or in GitHub Actions secrets.

To enable model responses, add the OpenAI API key only to the VM environment:

```sh
ssh opc@79.76.109.43
cd /opt/nova
sudoedit .env
docker compose --env-file .env -f docker-compose.prod.yml up -d api worker
```

Set `OPENAI_API_KEY` in that file and keep the existing daily and monthly budget limits. Do not paste the key into source files, commits, logs, or browser-visible frontend configuration.

## Domain and Cloudflare

The live public URL is `https://nova-app.i-shevchhuukk.workers.dev`. A small Cloudflare Worker terminates HTTPS and forwards requests to the Oracle VM. Its versioned source and Wrangler configuration are in `infrastructure/cloudflare/`.

Cloudflare Workers cannot fetch a numeric IP directly, so the Worker uses `nova.79-76-109-43.sslip.io`, which resolves to the VM IP. The stable user-facing address remains the `workers.dev` URL. The VM must keep:

```dotenv
DOMAIN=:80
WEB_ORIGIN=https://nova-app.i-shevchhuukk.workers.dev
```

To deploy a Worker change after authenticating Wrangler:

```sh
cd infrastructure/cloudflare
npx wrangler deploy
```

If a custom domain is purchased later, attach it to the same Worker and replace `WEB_ORIGIN` with that HTTPS hostname. No application code change is required.

## Deploy and rollback

The workflow runs this on the VM:

```sh
cd /opt/nova
./deploy-prod.sh <commit-sha>
```

The script pulls the four images, waits for PostgreSQL and Redis, applies the idempotent schema, starts the app services, checks `/health`, and prunes only old unused images. It never removes named data volumes.

To roll back, run the same command with the previous successful commit SHA. Keep schema changes backward-compatible before rolling back an application image.

## Backups

Install the daily PostgreSQL dump job:

```sh
cd /opt/nova
chmod 700 backup-postgres.sh install-backup-cron.sh
./install-backup-cron.sh
```

Compressed dumps are stored in `/opt/nova/backups`, mode `600`, with seven days retained. Periodically test a restore on a separate database or VM.

## Operations

```sh
cd /opt/nova
docker compose --env-file .env -f docker-compose.prod.yml ps
docker compose --env-file .env -f docker-compose.prod.yml logs --tail=200 api
docker compose --env-file .env -f docker-compose.prod.yml logs --tail=200 worker
curl -fsS http://127.0.0.1/health
```

When troubleshooting, inspect Compose status and logs, VM disk/RAM, GHCR authentication, firewall rules, and Caddy logs. Never remove the PostgreSQL volume as a first response.

## First live deploy checklist

1. Confirm the VM public subnet has a route for `0.0.0.0/0` through an internet gateway.
2. Run `bootstrap-oracle-linux.sh` and reconnect over SSH.
3. Create `/opt/nova/.env` with the required values above.
4. Push to `dev` or run the `deploy-dev` workflow manually.
5. Verify `http://SERVER_IP/health` before placing Cloudflare in front of the origin.
