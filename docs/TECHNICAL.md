# Technical Overview

## Stack

- **Backend** — Go 1.21, standard library `net/http` (no web framework), `github.com/lib/pq` as the Postgres driver, `golang.org/x/crypto` for password hashing.
- **Database** — PostgreSQL 15.
- **Frontend** — Vanilla JS SPA (hash-routed, no framework or bundler). Three static files served by the Go backend: `index.html`, `styles.css`, `app.js`.
- **Containerisation** — Docker + Docker Compose. Two services: `voting-db` (Postgres) and `voting-backend` (Go).
- **Build / dev workflow** — Makefile at the repo root (`docker compose` targets) and inside `backend/` (local Go build targets).

## Repository layout

```
Voting/
├── .env.example                # Required env vars — copy to .env and fill in
├── docker-compose.yml          # Orchestrates db + backend
├── Makefile                    # Top-level dev commands (make run, make logs, …)
├── docs/
│   ├── VISION.md               # Product vision & objective
│   └── TECHNICAL.md            # This file
├── postgres/
│   ├── Dockerfile
│   └── srcs/schema.sql         # Database schema, applied at container init
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
        ├── models.go           # Shared structs
        └── errors.go           # Sentinel errors
```

The backend splits **handlers** (HTTP-shaped logic) from **storage** (DB-shaped logic) so the data layer can evolve independently of the API surface.

## Data model

Defined in `postgres/srcs/schema.sql`. Core tables:

- `users` — registered accounts (username, email, password hash).
- `sessions` — opaque bearer tokens with a 30-day sliding expiry. Every authenticated request extends the session.
- `events` — top-level awards events. Has a `host_id`, `visibility` (`public` | `invite-only`), `is_active` flag, and `require_full_ballot` flag.
- `event_members` — join table tracking which users have joined which events. Required before voting.
- `invitations` — per-event invite tokens, issued by the host and redeemed by a user to join an invite-only event.
- `categories` — sub-votes inside an event (e.g. *Game of the Year*).
- `options` — candidates inside a category.
- `votes` — one row per cast vote. `UNIQUE(category_id, user_id)` enforces the one-vote-per-category rule at the DB level.

All foreign keys cascade on delete from the parent (event → categories → options → votes). Indexes are defined on every foreign key used in lookups.

## API surface

Routed in `backend/srcs/routes.go`:

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/auth/register` | — | Create account |
| `POST` | `/auth/login` | — | Login, returns session token |
| `POST` | `/auth/logout` | ✓ | Invalidate session |
| `GET` | `/auth/me` | ✓ | Validate token, return current user |
| `GET` | `/events` | — | List public events + user's events (with `is_member` flag) |
| `POST` | `/events` | ✓ | Create event |
| `GET` | `/events/{id}` | — | Event detail (with `is_member` + `my_votes` when authed) |
| `DELETE` | `/events/{id}` | ✓ host | Delete event |
| `POST` | `/events/{id}/close` | ✓ host | Close event |
| `POST` | `/events/{id}/join` | ✓ | Join a public event |
| `POST` | `/events/{id}/invitations` | ✓ host | Create invite token |
| `POST` | `/invitations/{token}` | ✓ | Redeem invite token |
| `POST` | `/votes` | ✓ | Cast a vote |
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

Backend listens on `:8080`, Postgres on `:5432`. The frontend is reachable at `http://localhost:8080/`.

## Design notes & conventions

- **No web framework** — routing is a `switch` on `path` + `method` in `RouteHandler`.
- **Storage layer is plain `database/sql`** — no ORM. Queries live in `*_storage.go` files.
- **DB enforces invariants** — uniqueness (one vote per category per user, unique invite tokens, unique event membership) is enforced in SQL. Handlers rely on the DB to reject duplicates.
- **Session sliding** — every authenticated request extends the session TTL by 30 days via an `UPDATE … RETURNING` pattern.
- **Schema changes** — the schema is applied from `schema.sql` at container init on a fresh volume. Run `make clean && make run` to reset. A migration tool should be adopted before any production deployment.
- **Frontend** — single-page app using the browser's hash for routing. No bundler; the three files are served as-is by the Go backend.
