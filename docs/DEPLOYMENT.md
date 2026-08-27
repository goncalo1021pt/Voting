# Deployment

How the Events app is hosted on the home lab (PowerEdge R620 → Proxmox VE).

It runs as a Docker Compose stack on a VM **shared with other apps**, and is
published to the internet at **https://voting.fontao.net** through a **Cloudflare
Tunnel** — so there are **no open ports** on the home router.

```
 user ──▶ voting.fontao.net ──▶ Cloudflare edge
                                      │
                                      │  outbound tunnel "voting"
                                      ▼
                        VM "apps" (192.168.1.70)
                         └─ docker compose — project "events"
                            ├─ events-cloudflared ──┐   connector, no ports
                            ├─ events-backend :8080 ◀┘  API + frontend
                            └─ events-db               Postgres, named volume
```

The Go backend serves **both the API and the static frontend** on `:8080`, and the
Compose stack is fully self-contained (backend + Postgres + connector).

The connector reaches the backend **over the Compose network** at `backend:8080`,
so serving needs **no host port at all**. The stack does publish
`127.0.0.1:8081`, but only for debugging over SSH — see *The shared VM* below for
why 8081 and not 8080.

---

## Target environment

| Thing | Value |
|---|---|
| Host | Proxmox VE on PowerEdge R620 (`pve`, `192.168.1.5`) |
| VM | `apps` — VM **200**, Debian 13 (cloud-init), static **192.168.1.70**, 4 GB / 2 cores / ~32 GB |
| Login | user `goncalo` (SSH key + cloud-init password), passwordless `sudo` |
| Runtime | Docker Engine + Compose plugin |
| App dir | `~/events` on the VM |
| Public URL | https://voting.fontao.net (Cloudflare Tunnel, no open ports) |

---

## The shared VM

`apps` is **not dedicated to this app**. It also runs, as separate Compose
projects, the `questboard` stack (the DnD_Helper app, published at
`dnd.fontao.net`), a `homelab-observability` stack (Grafana `:3000`, Prometheus
`:9090`, cadvisor, node-exporter, postgres-exporter), and a GitHub Actions
runner registered to the **DnD_Helper** repo — not to this one.

Two consequences worth knowing before you touch anything:

- **`127.0.0.1:8080` belongs to `questboard-app-1`.** This stack publishes
  `127.0.0.1:8081` instead. That host port is for on-box debugging only; public
  traffic never uses it, so the number is free to change if it ever clashes
  again. The container still listens on `8080` internally — container ports live
  in their own namespace and never clash.
- **Each app has its own tunnel**, not one shared connector: `dnd`, `dnd-test`,
  and `voting`. Adding this app does not touch questboard's tunnel, and
  restarting this stack cannot take `dnd.fontao.net` down.

Check what else is on the box before assuming a port or a name is free:

```bash
docker ps --format 'table {{.Names}}\t{{.Ports}}'
ss -tlnp
```

---

## 1. The VM (one-time, on the Proxmox host)

Created from the Debian 13 generic cloud image with cloud-init (static IP, SSH key):

```bash
# on the Proxmox host (192.168.1.5)
IMG=/var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2   # downloaded from cloud.debian.org
qm create 200 --name apps --memory 4096 --cores 2 --cpu host \
  --net0 virtio,bridge=vmbr0 --scsihw virtio-scsi-single --ostype l26 --agent enabled=1
qm importdisk 200 "$IMG" local-lvm
qm set 200 --scsi0 local-lvm:vm-200-disk-0,discard=on,ssd=1
qm set 200 --ide2 local-lvm:cloudinit
qm set 200 --boot order=scsi0
qm set 200 --serial0 socket --vga serial0
qm set 200 --ciuser goncalo --cipassword '<password>' --sshkeys /root/<your>.pub
qm set 200 --ipconfig0 ip=192.168.1.70/24,gw=192.168.1.1 --nameserver 1.1.1.1
qm resize 200 scsi0 +30G
qm start 200
```

## 2. Docker (one-time, in the VM)

```bash
ssh goncalo@192.168.1.70
sudo apt-get update
curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker goncalo   # log out/in for the group to apply
```

## 3. The app (deploy / redeploy)

Get the code onto the VM (clone from GitHub, or copy from your machine):

```bash
# option A: clone
git clone git@github.com:goncalo1021pt/Voting.git ~/events

# option B: copy a local working tree (tar over ssh)
#   tar czf - --exclude=.git --exclude=.env . | ssh goncalo@192.168.1.70 'mkdir -p ~/events && tar xzf - -C ~/events'
```

Create `~/events/.env` (see `.env.example`) with a **strong** `DB_PASSWORD`:

```bash
cd ~/events
cat > .env <<EOF
DB_HOST=postgres
DB_PORT=5432
DB_NAME=events_db
DB_USER=events_user
DB_PASSWORD=$(openssl rand -base64 18 | tr -d '/+=' | head -c 24)
EOF
```

Then append the tunnel connector token (see step 4 for where to get it):

```bash
echo "TUNNEL_TOKEN=<token from the Zero Trust dashboard>" >> .env
```

Build and start:

```bash
make prod                              # compose --profile tunnel up -d --build
docker compose ps                      # backend + db healthy, cloudflared up
curl -sI http://localhost:8081/        # expect HTTP 200
curl -s  http://localhost:8081/healthz # expect: ok
```

The backend reports `healthy` once `/healthz` can reach the database, which
takes a few seconds after `up` (the healthcheck has a 20s start period). The
connector waits for that healthy state before dialling out, so the tunnel never
advertises an origin that cannot serve.

> `make up` starts the stack **without** the connector — that is the dev path.
> Production is `make prod`. The split exists so a laptop running `make up`
> never starts answering for the production hostname.

## 4. Public access — Cloudflare Tunnel (one-time)

`cloudflared` runs **as a container in this stack** and dials **out** to
Cloudflare, so nothing inbound is opened. Requires `fontao.net` to be on
Cloudflare.

This is a **remotely-managed (token) tunnel**: the hostname → service mapping
lives in the Cloudflare Zero Trust dashboard, **not** in a local
`/etc/cloudflared/config.yml`. The VM holds only a connector token. There is
nothing to install on the host with `apt` and no `systemd` unit — the tunnel is
just another service in `docker-compose.yml`.

In the dashboard — **Zero Trust → Networks → Tunnels** — the tunnel named
**`voting`** already exists, with `voting.fontao.net` routed to it. To (re)create
that state from scratch:

1. **Create a tunnel** named `voting`, type `cloudflared`.
2. **Add a public hostname**: subdomain `voting`, domain `fontao.net`, service
   **`HTTP`** → **`backend:8080`**. Saving it creates the proxied DNS record
   automatically (it shows in the DNS tab as type **Tunnel**, not CNAME).
3. **Copy the connector token** from the tunnel's **Docker** install tab — the
   long `eyJhIjoi…` string in the sample command — into `TUNNEL_TOKEN` in
   `~/events/.env`.

The service must be **`backend:8080`**, not `localhost:8080`: the connector runs
in a container, so `localhost` is that container, not the VM. It resolves
`backend` over the Compose network — which is also why this app needs no host
port to serve.

Then start it (step 3's `make prod` already does this):

```bash
docker compose --profile tunnel up -d
docker compose logs -f cloudflared     # expect: "Registered tunnel connection"
```

The dashboard should flip the tunnel from **Down** to **Healthy** with 1 active
replica within a few seconds.

> **Where the domain is defined:** in two coupled places — the **DNS record**
> `voting.fontao.net` (type *Tunnel*, proxied) and the tunnel's **public
> hostname** entry, tied together by the tunnel **UUID**. Both are created for
> you when you add the public hostname; you rarely touch the DNS tab directly.
>
> A hostname whose tunnel has no running connector answers **HTTP 530 (error
> 1033)** — that is "the route exists, nothing is home", and it is what
> `voting.fontao.net` served before this deploy. If you see 1033 later, check
> `docker compose ps cloudflared` first.
>
> One tunnel *can* serve many hostnames, but this home lab runs **one tunnel per
> app** so each stack owns its own public entrypoint and cannot break its
> neighbours.
>
> Because the tunnel is outbound, the app needs **no port-forward and no DDNS** —
> a changing home IP never breaks it. (Contrast: game servers, which do need those.)

---

## Updating the app

```bash
ssh goncalo@192.168.1.70
cd ~/events
git pull                      # or re-copy the working tree
make prod                     # rebuild + restart with zero config changes
docker compose logs -f backend
```

## Database migrations

The schema is owned by [goose](https://github.com/pressly/goose) migrations in
`backend/srcs/migrations/`. They are **embedded in the backend binary** and run
automatically at startup, before the server accepts traffic — so
`make prod` is the whole migration procedure. If a migration
fails the backend exits rather than serving against a schema it doesn't expect;
`docker compose logs backend` has the SQL error.

Nothing special is needed for the existing production database. Migration
`00001_baseline` is written entirely with `IF NOT EXISTS`, so it no-ops against a
database that the old `schema.sql` init already populated, and `00002` adds the
`invitations.expires_at` column that such a database is missing.

**Take a backup before deploying a migration** (`make backup`) — goose has `Down`
steps, but restoring a dump is the reliable rollback.

Check the applied version:

```bash
docker compose exec postgres psql -U events_user -d events_db \
  -c 'SELECT version_id, is_applied, tstamp FROM goose_db_version ORDER BY id DESC LIMIT 5;'
```

### Adding a migration

Create `backend/srcs/migrations/<NNNNN>_<short_name>.sql`, numbered one above the
highest existing file:

```sql
-- +goose Up
ALTER TABLE events ADD COLUMN IF NOT EXISTS archived_at TIMESTAMP;

-- +goose Down
ALTER TABLE events DROP COLUMN IF EXISTS archived_at;
```

Rules that keep deploys boring:

- **Never edit a migration that has been deployed.** Goose records which
  versions ran; changing one in place means prod and fresh databases silently
  diverge. Add a new migration instead.
- **Prefer additive, backwards-compatible changes** — add nullable columns, then
  backfill, then tighten. The old binary keeps running during a rebuild.
- Keep `IF NOT EXISTS` / `IF EXISTS` guards so a re-run is harmless.

## Operations

```bash
docker compose ps                       # status
docker compose logs -f                  # live logs (all services)
docker compose restart backend          # restart just the backend
docker compose down                     # stop (keeps the DB volume)
docker compose logs -f cloudflared      # tunnel health (no systemd unit)
```

### Health

`GET /healthz` pings the database and returns `200 ok` or `503 database
unreachable`, so "healthy" means the backend can actually serve — not merely
that the process is listening. Compose probes it every 10s:

```bash
curl -s http://localhost:8081/healthz
docker compose ps                       # backend shows (healthy) / (unhealthy)
docker inspect --format '{{.State.Health.Status}}' events-backend
```

> **A failing healthcheck does not restart the container.** Plain Docker only
> acts on `restart:` policies when a container *exits*; restarting on an
> unhealthy status is a Swarm feature. What you get here is an accurate
> `docker compose ps`, a signal for monitoring to alert on, and a gate for
> `depends_on: service_healthy`. Restart a wedged backend by hand with
> `docker compose restart backend`.

Successful probes are deliberately kept out of the access log — at every 10s
they'd add ~8,600 lines a day. Failing ones are logged with the cause.

### Reading the logs

The backend writes one line per request:

```
2026/08/08 15:42:51 req=3ca2fd01 GET /events 500 6.11ms 23B ip=203.0.113.7
```

`req=` is a per-request ID, also returned to the client as the `X-Request-Id`
header. A 500 logs a second line under the same ID with the underlying cause —
the client only ever sees the generic message:

```
2026/08/08 15:42:51 req=3ca2fd01 ERROR GET /events: Failed to fetch events: dial tcp: lookup postgres: no such host
```

So when someone reports an error, ask for the `X-Request-Id` from their browser's
network tab and:

```bash
docker compose logs backend | grep req=3ca2fd01   # both lines for that request
docker compose logs backend | grep ERROR          # every 500 since startup
docker compose logs backend | grep WARN           # rows the storage layer skipped
```

Invitation tokens are redacted from logged paths (`/invitations/***`) because
they grant access to an invite-only event; query strings are never logged.

### Database backup / restore
Postgres data lives in the named volume `events_postgres_data`.

Manual:

```bash
make backup   # writes backups/events_<timestamp>.sql
# restore (into a running db)
cat backups/events_<timestamp>.sql | docker compose exec -T postgres psql -U events_user -d events_db
```

Automated nightly backups — a systemd timer runs `deploy/backup.sh`, which
gzips a dump into `backups/` and keeps the newest 14:

```bash
sudo cp deploy/events-backup.service deploy/events-backup.timer /etc/systemd/system/
sudoedit /etc/systemd/system/events-backup.service   # set WorkingDirectory to this repo's path
sudo systemctl daemon-reload
sudo systemctl enable --now events-backup.timer
sudo systemctl start events-backup.service           # run one now to test
systemctl list-timers events-backup.timer            # confirm the schedule
```

Backups on the same disk don't survive the disk. Copy `backups/` offsite —
e.g. an `rclone` sync to object storage or a nightly `scp` to another machine
— before relying on them, and test a restore once.

---

## Notes & hardening

- **No inbound ports** are opened on the router — exposure is entirely via the
  Cloudflare Tunnel (outbound). Management is LAN-only / over SSH.
- Postgres has **no host port mapping** — it is reachable only on the internal
  Compose network. The backend publishes on **`127.0.0.1:8081`** for debugging
  only — public traffic arrives over the Compose network instead — so the LAN
  cannot bypass Cloudflare's TLS, WAF and rate rules by talking to it directly.
- **`CF-Connecting-IP` is only believed from a trusted peer.** It decides the
  rate-limit bucket and what lands in the access log, so a caller who could
  set it freely could present a new IP per request and make the auth limiter
  useless. Trusted peers default to loopback plus the Docker bridge ranges
  (`127.0.0.0/8,::1/128,172.16.0.0/12`), which covers the Compose network that
  the cloudflared container arrives from, and anything else falls back to the real socket address. Override
  with `TRUSTED_PROXY_CIDRS` if the topology changes; an unparseable value
  fails the boot rather than quietly changing who is trusted.

  > If you ever republish the backend beyond loopback, revisit this list first.
  > Trusting a range you don't control hands out rate-limit immunity.
- **Security headers** are set on every response — CSP (scripts limited to
  same-origin plus a per-request nonce), `nosniff`, `Referrer-Policy`,
  `X-Frame-Options: DENY`, `Cross-Origin-Opener-Policy`. Check them with:

  ```bash
  curl -sI https://voting.fontao.net/ | grep -i -E 'content-security|x-frame|nosniff|referrer'
  ```

  Adding a third-party script, font or image source means widening the CSP in
  `backend/srcs/security.go` — the browser will silently refuse to load it
  otherwise. Watch the browser console after any frontend change that pulls in
  something new.
- **CORS is off** unless `ALLOWED_ORIGINS` is set (comma-separated). The
  frontend is served from this same origin, so it needs none. Set it only if
  another origin must call the API, and list exact origins — the old
  `Access-Control-Allow-Origin: *` applied to authenticated responses too.
- **Rate limits** are per-endpoint and in-process (auth and event creation
  10 per 5 min, invitation redemption 20, invites and voting 60). Authenticated
  callers are counted per user, anonymous ones per IP. A 429 carries
  `Retry-After: 300`. They live in `backend/srcs/middleware.go` — a restart
  clears all counters, and with more than one backend container each would keep
  its own tally, so Cloudflare rate rules remain the outer layer.
- **HSTS is not set by the backend.** Cloudflare terminates TLS, so enable
  *Strict Transport Security* there (SSL/TLS → Edge Certificates) rather than
  emitting it from an origin that also answers plain HTTP on localhost.
- Keep `.env` out of git (it holds `DB_PASSWORD`); it's already in `.gitignore`.
- The VM's cloud-init console password should be changed from its initial value
  (`passwd` on the VM); SSH-key login is the primary access path.
