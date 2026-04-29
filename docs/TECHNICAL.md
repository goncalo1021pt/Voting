# Technical Overview

## Stack

- **Backend** — Go 1.21, standard library `net/http` (no web framework), `github.com/lib/pq` as the Postgres driver, `golang.org/x/crypto` for password hashing.
- **Database** — PostgreSQL.
- **Frontend** — single static `index.html` served by the Go backend. No SPA framework yet.
- **Containerization** — Docker + Docker Compose. Two services: `voting-db` (Postgres) and `voting-backend` (Go).
- **Build / dev workflow** — Makefile at the repo root and inside `servers/backend/`.

## Repository layout

```
Voting/
├── docker-compose.yml          # Orchestrates db + backend
├── Makefile                    # Top-level dev commands
├── docs/
│   ├── VISION.md               # Product vision & objective
│   └── TECHNICAL.md            # This file
└── servers/
    ├── postgres/
    │   ├── Dockerfile
    │   └── srcs/schema.sql     # Database schema, applied at container init
    └── backend/
        ├── Dockerfile
        ├── Makefile
        ├── go.mod / go.sum
        ├── frontend/
        │   └── index.html      # Static UI served at /
        └── srcs/
            ├── main.go             # Entry point, server bootstrap
            ├── routes.go           # HTTP routing + CORS middleware
            ├── auth.go             # Register / login / logout / RequireAuth
            ├── auth_storage.go     # User & session DB access
            ├── event_handlers.go   # Event / category / option / vote handlers
            ├── event_storage.go    # Event-related DB access
            ├── db.go               # DB connection lifecycle
            ├── models.go           # Shared structs
            └── errors.go           # Error helpers
```

The backend deliberately splits **handlers** (HTTP-shaped logic) from **storage** (DB-shaped logic) so the data layer can evolve independently of the API surface.

## Data model

Defined in `servers/postgres/srcs/schema.sql`. Core tables:

- `users` — registered accounts (username, email, password hash).
- `events` — top-level awards events. Has a `host_id`, `visibility` (`public` | `invite-only`), and `is_active` flag.
- `event_members` — join table tracking which users have joined which events. Required before voting.
- `invitations` — per-event invite tokens, issued by the host and redeemed by a user to join an invite-only event.
- `categories` — sub-votes inside an event (e.g. *Game of the Year*).
- `options` — candidates inside a category.
- `votes` — one row per cast vote. `UNIQUE(category_id, user_id)` enforces the v1 "one vote per category per user" rule at the DB level.

All foreign keys cascade on delete from the parent (event → categories → options → votes), so removing an event cleans up its full subtree. Indexes are defined on every foreign key used in lookups.

## API surface (current)

Routed in `servers/backend/srcs/routes.go`:

- `POST /auth/register`, `POST /auth/login`, `POST /auth/logout`
- `GET /events`, `POST /events` *(auth)*
- `GET /events/{id}`
- `POST /events/{id}/invitations` *(auth, host only)*
- `POST /invitations/{token}` — redeem an invite to join
- `POST /votes` *(auth)*
- `GET /events/{id}/results/...`
- `GET /` — serves the static frontend

`RequireAuth` wraps handlers that need a logged-in user. `CORSMiddleware` wraps the whole mux.

## Configuration

Runtime config is environment-driven via `docker-compose.yml`:

- `DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME` — Postgres connection.
- `ADMIN_TOKEN` — privileged operations.

Secrets are read from a `.env` file at the repo root (gitignored).

## Running locally

```bash
docker compose up --build
```

Backend listens on `:8080`, Postgres on `:5432`. The frontend is reachable at `http://localhost:8080/`.

## Design notes & conventions

- **No web framework** — routing is a `switch` on `path` + `method` in `RouteHandler`. Keeps the dependency surface tiny; if routing complexity grows we can revisit.
- **Storage layer is plain `database/sql`** — no ORM. Queries live next to the handlers that use them, in `*_storage.go`.
- **DB enforces invariants** — uniqueness (one vote per category per user, unique invite tokens, unique event membership) is enforced in SQL, not just in Go. Handlers can rely on the DB to reject duplicates.
- **Schema changes** — for now, the schema is re-applied from `schema.sql` on a fresh container. A proper migration tool will be needed before the first real deployment.
