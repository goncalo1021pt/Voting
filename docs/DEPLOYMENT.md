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
docker compose up -d --build      # or: make up
docker compose ps                 # backend healthy + db healthy
curl -sI http://localhost:8080/   # expect HTTP 200
```

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

## Operations

```bash
docker compose ps                       # status
docker compose logs -f                  # live logs (all services)
docker compose restart backend          # restart just the backend
docker compose down                     # stop (keeps the DB volume)
sudo systemctl status cloudflared       # tunnel health
```

### Database backup / restore
Postgres data lives in the named volume `events_postgres_data`.

```bash
# backup
docker compose exec -T postgres pg_dump -U events_user events_db > backup_$(date +%F).sql
# restore (into a running db)
cat backup_YYYY-MM-DD.sql | docker compose exec -T postgres psql -U events_user -d events_db
```

---

## Notes & hardening

- **No inbound ports** are opened on the router — exposure is entirely via the
  Cloudflare Tunnel (outbound). Management is LAN-only / over SSH.
- `docker-compose.yml` currently publishes Postgres `5432` on the VM's LAN
  interface. It is **not** internet-exposed (only `:8080` is tunnelled), but for
  defense-in-depth you can drop the `ports:` mapping on the `postgres` service so the
  DB is reachable only on the internal Compose network.
- Keep `.env` out of git (it holds `DB_PASSWORD`); it's already in `.gitignore`.
- The VM's cloud-init console password should be changed from its initial value
  (`passwd` on the VM); SSH-key login is the primary access path.
