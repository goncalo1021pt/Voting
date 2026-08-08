# Deployment

How the Events app is hosted on the home lab (PowerEdge R620 → Proxmox VE).

It runs as a Docker Compose stack inside a dedicated VM, and is published to the
internet at **https://vote.fontao.net** through a **Cloudflare Tunnel** — so there
are **no open ports** on the home router.

```
 user ──▶ vote.fontao.net ──▶ Cloudflare edge ──(outbound tunnel)──▶ VM "apps" (192.168.0.70)
                                                                       └─ docker compose
                                                                          ├─ events-backend  :8080  (API + frontend)
                                                                          └─ events-db        (Postgres, named volume)
```

The Go backend serves **both the API and the static frontend** on `:8080`, and the
Compose stack is fully self-contained (backend + Postgres), so deployment is just:
run the stack, then point a tunnel hostname at `:8080`.

---

## Target environment

| Thing | Value |
|---|---|
| Host | Proxmox VE on PowerEdge R620 (`pve`, `192.168.0.5`) |
| VM | `apps` — VM **200**, Debian 13 (cloud-init), static **192.168.0.70**, 4 GB / 2 cores / ~32 GB |
| Login | user `goncalo` (SSH key + cloud-init password), passwordless `sudo` |
| Runtime | Docker Engine + Compose plugin |
| App dir | `~/events` on the VM |
| Public URL | https://vote.fontao.net (Cloudflare Tunnel, no open ports) |

---

## 1. The VM (one-time, on the Proxmox host)

Created from the Debian 13 generic cloud image with cloud-init (static IP, SSH key):

```bash
# on the Proxmox host (192.168.0.5)
IMG=/var/lib/vz/template/iso/debian-13-genericcloud-amd64.qcow2   # downloaded from cloud.debian.org
qm create 200 --name apps --memory 4096 --cores 2 --cpu host \
  --net0 virtio,bridge=vmbr0 --scsihw virtio-scsi-single --ostype l26 --agent enabled=1
qm importdisk 200 "$IMG" local-lvm
qm set 200 --scsi0 local-lvm:vm-200-disk-0,discard=on,ssd=1
qm set 200 --ide2 local-lvm:cloudinit
qm set 200 --boot order=scsi0
qm set 200 --serial0 socket --vga serial0
qm set 200 --ciuser goncalo --cipassword '<password>' --sshkeys /root/<your>.pub
qm set 200 --ipconfig0 ip=192.168.0.70/24,gw=192.168.0.1 --nameserver 1.1.1.1
qm resize 200 scsi0 +30G
qm start 200
```

## 2. Docker (one-time, in the VM)

```bash
ssh goncalo@192.168.0.70
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
#   tar czf - --exclude=.git --exclude=.env . | ssh goncalo@192.168.0.70 'mkdir -p ~/events && tar xzf - -C ~/events'
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

Build and start:

```bash
docker compose up -d --build          # or: make up
docker compose ps                     # backend healthy + db healthy
curl -sI http://localhost:8080/       # expect HTTP 200
curl -s  http://localhost:8080/healthz # expect: ok
```

The backend reports `healthy` once `/healthz` can reach the database, which
takes a few seconds after `up` (the healthcheck has a 20s start period).

## 4. Public access — Cloudflare Tunnel (one-time)

`cloudflared` runs in the VM and dials **out** to Cloudflare, so nothing inbound is
opened. Requires `fontao.net` to be on Cloudflare.

```bash
# install
curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o /tmp/cf.deb
sudo dpkg -i /tmp/cf.deb

# authenticate (opens a browser URL; authorize the fontao.net zone)
cloudflared tunnel login

# create the tunnel + route the hostname
cloudflared tunnel create events
cloudflared tunnel route dns events vote.fontao.net
```

Config at **`/etc/cloudflared/config.yml`** (the hostname → local service mapping):

```yaml
tunnel: <TUNNEL-UUID>
credentials-file: /etc/cloudflared/<TUNNEL-UUID>.json

ingress:
  - hostname: vote.fontao.net
    service: http://localhost:8080
  - service: http_status:404
```

Install as a service:

```bash
sudo cloudflared service install
sudo systemctl enable --now cloudflared
```

> **Where the domain is defined:** in two coupled places — the **Cloudflare DNS
> CNAME** `vote.fontao.net → <UUID>.cfargotunnel.com` (created by `tunnel route dns`)
> and the **`ingress`** block in `/etc/cloudflared/config.yml`. They're tied together
> by the tunnel **UUID**. One tunnel can serve many hostnames — add more `ingress`
> rules (+ a `route dns` each) to publish other apps on this VM.
>
> Because the tunnel is outbound, the app needs **no port-forward and no DDNS** —
> a changing home IP never breaks it. (Contrast: game servers, which do need those.)

---

## Updating the app

```bash
ssh goncalo@192.168.0.70
cd ~/events
git pull                      # or re-copy the working tree
docker compose up -d --build  # rebuild + restart with zero config changes
docker compose logs -f backend
```

## Database migrations

The schema is owned by [goose](https://github.com/pressly/goose) migrations in
`backend/srcs/migrations/`. They are **embedded in the backend binary** and run
automatically at startup, before the server accepts traffic — so
`docker compose up -d --build` is the whole migration procedure. If a migration
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
sudo systemctl status cloudflared       # tunnel health
```

### Health

`GET /healthz` pings the database and returns `200 ok` or `503 database
unreachable`, so "healthy" means the backend can actually serve — not merely
that the process is listening. Compose probes it every 10s:

```bash
curl -s http://localhost:8080/healthz
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
  Compose network. The backend's `8080` *is* published on the VM's LAN
  interface; binding it to `127.0.0.1` so only the tunnel can reach it is
  tracked in issue #38.
- Keep `.env` out of git (it holds `DB_PASSWORD`); it's already in `.gitignore`.
- The VM's cloud-init console password should be changed from its initial value
  (`passwd` on the VM); SSH-key login is the primary access path.
