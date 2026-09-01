# Technical Overview

## Stack

- **Backend** — Go 1.26, standard library `net/http` (no web framework), `github.com/lib/pq` as the Postgres driver, `golang.org/x/crypto` for password hashing.
- **Database** — PostgreSQL 15.
- **Frontend** — Vanilla JS SPA (hash-routed, no framework or bundler). Three static files served by the Go backend: `index.html`, `styles.css`, `app.js`.
- **Containerisation** — Docker + Docker Compose. Two services: `events-db` (Postgres) and `events-backend` (Go).
- **Build / dev workflow** — Makefile at the repo root (`docker compose` targets) and inside `backend/` (local Go build targets).

## Repository layout

```
events/
├── .env.example                # Required env vars — copy to .env and fill in
├── docker-compose.yml          # Orchestrates db + backend
├── Makefile                    # Top-level dev commands (make run, make logs, …)
├── docs/
│   ├── VISION.md               # Product vision & objective
│   └── TECHNICAL.md            # This file
├── postgres/
│   └── Dockerfile
├── frontend/
│   ├── index.html              # Shell — topbar, view mount point, theme bootstrap
│   ├── styles.css              # Editorial / awards-show design system
│   └── app.js                  # Full SPA: router, views, API client, DOM helpers
└── backend/
    ├── Dockerfile
    ├── Makefile                # Local Go build targets
    ├── go.mod / go.sum
    └── srcs/
        ├── main.go             # Entry point, server bootstrap
        ├── routes.go           # HTTP routing + CORS middleware
        ├── auth.go             # Register / login / logout / me / RequireAuth
        ├── auth_storage.go     # User & session DB access
        ├── event_handlers.go   # Event / category / option / vote / results handlers
        ├── event_storage.go    # Event-related DB access
        ├── db.go               # DB connection lifecycle
        ├── migrate.go          # Runs embedded goose migrations at startup
        ├── migrations/         # Numbered .sql migrations — the schema's source of truth
        ├── models.go           # Shared structs
        └── errors.go           # Sentinel errors
```

The backend splits **handlers** (HTTP-shaped logic) from **storage** (DB-shaped logic) so the data layer can evolve independently of the API surface.

## Data model

Defined by the migrations in `backend/srcs/migrations/`. Core tables:

- `users` — registered accounts (username, email, password hash).
- `sessions` — opaque bearer tokens with a 30-day sliding expiry. Every authenticated request extends the session.
- `events` — top-level awards events. Has a `host_id`, `visibility` (`public` | `invite-only`), `is_active` flag, and `require_full_ballot` flag.
- `event_members` — join table tracking which users have joined which events. Required before voting.
- `invitations` — per-event invite tokens, issued by the host and redeemed by users to join an invite-only event. Two nullable limits, both using NULL for "no limit": `expires_at` (NULL = never expires) and `max_uses` (NULL = unlimited, so one link can be posted in a group chat). Expired and used-up tokens are rejected at redemption.
- `invitation_redemptions` — one row per person a link admitted, with `UNIQUE(invitation_id, user_id)` so a re-clicked link cannot count twice against the cap. This is what replaced the single `redeemed_by`/`redeemed_at` pair, which could only ever describe one redeemer.
- `categories` — sub-votes inside an event (e.g. *Game of the Year*).
- `options` — candidates inside a category.
- `votes` — one row per cast vote. `UNIQUE(category_id, user_id)` enforces the one-vote-per-category rule at the DB level.

All foreign keys cascade on delete from the parent (event → categories → options → votes). Indexes are defined on every foreign key used in lookups.

## API surface

Routed in `backend/srcs/routes.go`:

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `GET` | `/healthz` | — | Liveness + DB reachability; `200 ok` or `503` |
| `POST` | `/auth/register` | — | Create account |
| `POST` | `/auth/login` | — | Login, returns session token |
| `POST` | `/auth/logout` | ✓ | Invalidate session |
| `GET` | `/auth/me` | ✓ | Validate token, return current user |
| `PATCH` | `/auth/me` | ✓ | Change your own username; body `{"username": "..."}` |
| `GET` | `/events` | — | List public events + user's events (with `is_member` flag) |
| `POST` | `/events` | ✓ | Create event |
| `GET` | `/events/{id}` | — | Event detail (with `is_member` + `my_votes` when authed) |
| `PUT` | `/events/{id}` | ✓ host | Edit event; body is the whole event, ids kept on rows to keep |
| `DELETE` | `/events/{id}` | ✓ host | Delete event |
| `POST` | `/events/{id}/close` | ✓ host | Close event |
| `POST` | `/events/{id}/join` | ✓ | Join a public event |
| `POST` | `/events/{id}/invitations` | ✓ host | Create invite token; optional body `{"expires_in_hours": 1–8760, "max_uses": 1–10000 \| null}` — `max_uses` absent means single use, `null` means unlimited |
| `GET` | `/events/{id}/invitations` | ✓ host | List invitations with `uses` and the `redemptions` behind them |
| `DELETE` | `/events/{id}/invitations/{token}` | ✓ host | Revoke an invitation, used or not; people it already admitted stay members |
| `GET` | `/events/{id}/members` | ✓ host | List members (host first, then join order), each with `votes_cast` — how many categories they have voted in |
| `DELETE` | `/events/{id}/members/{userId}` | ✓ host | Remove a member; their cast votes are kept |
| `POST` | `/invitations/{token}` | ✓ | Redeem invite token; `already_member: true` when the caller was already in the event |
| `POST` | `/events/{id}/ballot` | ✓ | Cast a whole ballot atomically; the only way to vote on a `require_full_ballot` event |
| `POST` | `/votes` | ✓ | Cast one vote; 409 on a `require_full_ballot` event |
| `GET` | `/events/{id}/results` | — | All-categories results (gated by visibility rules) |
| `GET` | `/events/{id}/results/{catId}` | — | Single category results (gated by visibility rules) |
| `GET` | `/` | — | Static frontend (SPA fallback) |

`RequireAuth` wraps handlers that need a logged-in user. `CORSMiddleware` wraps the whole mux.

## Configuration

Driven by environment variables. Copy `.env.example` to `.env` and fill in values before running:

- `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` — Postgres connection.

## Running locally

```bash
cp .env.example .env   # fill in values (defaults in .env.example work out of the box)
make run               # builds images and starts containers in background
make logs              # tail logs from all services
make clean             # stop containers and wipe volumes (resets DB)
```

Backend listens on `:8080`, Postgres on `:5432`. The frontend is reachable at `http://localhost:8080/`. Compose publishes the backend on `127.0.0.1` only — in production `cloudflared` runs on the same host and is the sole ingress.

## Design notes & conventions

- **No web framework** — routing is a `switch` on `path` + `method` in `RouteHandler`.
- **Storage layer is plain `database/sql`** — no ORM. Queries live in `*_storage.go` files.
- **DB enforces invariants** — uniqueness (one vote per category per user, unique invite tokens, unique event membership) is enforced in SQL. Handlers rely on the DB to reject duplicates.
- **Storage errors propagate** — no loop skips a row on a scan failure and no `Scan` result goes unchecked. Serving an event with a category quietly missing looks like the host never created it, and a dropped result row understates a tally; both are worse than an error the caller can retry.
- **Authorization distinguishes its outcomes** — `requireHost` answers 404 for an event that doesn't exist, 403 only for a real event the caller doesn't host, and a logged 500 for a lookup failure.
- **An invitation can admit one person or a crowd** — `max_uses` NULL means unlimited, which is the link a host posts in a group chat; a number caps it; absent from the create body means single use, so a caller that predates the field gets what it always got. Redemptions live in their own table, and the count of them is what gates a capped link. Two details make that count trustworthy: the invitation row is taken `FOR UPDATE` before it is counted, and the count runs as its **own statement** — a waiter released by the locking statement re-reads the row it blocked on, but a subquery inside that same statement keeps the snapshot it started with, which let two concurrent redemptions both see room on a link with one use left.
- **Redeeming a link you don't need is not an error** — someone already in the event (the host following their own link, a second tap on the group chat message) is turned away with `ErrAlreadyMember` before any use is spent, and the endpoint answers 200 with `already_member: true` so the frontend can take them to the event instead of showing a failure. Letting this through burned a single-use invitation on somebody who was already inside.
- **Editing an event is a whole-event PUT** — `PUT /events/{id}` carries the event as it should end up, not a diff: a category or option with an `id` is renamed in place, one without is created, and anything left out is deleted. Votes reference option and category rows, so removing a row somebody has voted on would rewrite a tally rather than fix a mistake; the server answers 409 and rolls the whole edit back. Re-parenting an option to another category is refused for the same reason. The frontend's create and edit forms are one builder (`ballotEditor` in `app.js`), which is why the two pages stay in step.
- **Usernames are the user's to change** — `PATCH /auth/me` renames any account, including one created by Google sign-in, where the initial name is derived from the email address. Only the username moves: the Google subject and the provider email stay put, so a rename can't repoint an identity. `normalizeUsername` trims and collapses whitespace and bounds the result (3–32 characters, letters/digits/spaces/`. _ -`, starting and ending alphanumeric); `UNIQUE(username)` decides conflicts, surfaced as 409.
- **The roster says who is missing, not what anyone chose** — each member row carries `votes_cast`, a count of the event's categories that member has voted in, so a host staring at "15 of 16 voted" can name the missing one and go nudge them. It is a count and nothing else: no option ever appears, and the endpoint stays host-only. Zero means the same thing as the event's `voter_count` denominator, so the roster and the header tag can't tell two different stories. A member who filled in some categories but not all reads as a partial count, which is the case turnout hides — they are counted as a voter but still owe a ballot.
- **Ballots are atomic** — `require_full_ballot` cannot be expressed one vote at a time, so `POST /events/{id}/ballot` records the whole ballot in a single transaction: it lands completely or not at all. `POST /votes` stays for lenient events and returns 409 on a strict one. Votes cast earlier count toward completeness, so a voter part-way through can finish with a ballot covering only what's left.
- **Session sliding** — every authenticated request extends the session TTL by 30 days via an `UPDATE … RETURNING` pattern.
- **Security headers** — `SecurityHeadersMiddleware` sets a CSP plus `nosniff`, `Referrer-Policy: same-origin`, `X-Frame-Options: DENY` and `Cross-Origin-Opener-Policy: same-origin`. Scripts are restricted to `'self'` and a **per-request nonce**, injected into the shell's inline theme script by `serveIndex`; the session token lives in localStorage, so this is what keeps an XSS from becoming account takeover. `style-src` allows `'unsafe-inline'` because `el()` sets style attributes — inline CSS can't reach the token, and `style-src-attr` isn't portable enough to rely on.
- **CORS** — off unless `ALLOWED_ORIGINS` is set. The backend serves the frontend on the same origin and `app.js` fetches relative paths, so nothing legitimate needs it. When configured, the specific origin is echoed (never `*`) with `Vary: Origin`, and `Allow-Methods` advertises exactly what `RouteHandler` implements.
- **Rate limiting** — per-endpoint budgets in `middleware.go`, sized to what each call costs and what abusing it buys: auth and self-rename 10/5min, event creation and editing 10/5min (either can write ~20k rows), invitation redemption 20/5min (token guessing), invite creation and voting 60/5min (a live event means everyone submits at once). Authenticated requests key on **user id**, so rotating IPs buys no budget and a shared NAT doesn't punish bystanders; anonymous ones key on IP. On protected routes the limiter sits inside `RequireAuth` — putting it outside would mean resolving the session twice per write, since `GetUserFromToken` slides it. A background sweeper drops aged-out keys every 10 minutes.
- **Client IP** — `clientIP` believes `CF-Connecting-IP` only when the peer is in `TRUSTED_PROXY_CIDRS` (default: loopback + Docker bridge), and only when the value parses as an IP. It keys the auth rate limiter, so an untrusted caller able to set it freely would get a fresh bucket per request.
- **Logging** — `LogMiddleware` writes one access line per request and tags it with a short random request ID, echoed to the client as `X-Request-Id`. Every 500 goes through `serverError`, which logs the underlying cause under the same ID and sends only the generic message to the client. Invitation tokens are redacted out of logged paths (they're credentials); query strings are never logged.
- **Schema changes** — managed by [goose](https://github.com/pressly/goose). Migrations live in `backend/srcs/migrations/`, are embedded in the binary, and run automatically at startup before the server accepts traffic. See *Database migrations* in `DEPLOYMENT.md` for how to add one.
- **Frontend** — single-page app using the browser's hash for routing. No bundler; the three files are served as-is by the Go backend.
