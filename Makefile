all: build

build:
	docker compose build

up: build
	docker compose up -d
	@echo "Containers running. Access backend at http://localhost:8081"

# Production. Adds the cloudflared connector that serves voting.fontao.net;
# `up` deliberately leaves it out so a dev machine never answers for the
# public hostname. Needs TUNNEL_TOKEN in .env.
prod:
	docker compose --profile tunnel up -d --build
	@echo "Stack up with the tunnel. Public at https://voting.fontao.net"

down:
	docker compose down

logs:
	docker compose logs -f

# Dump the database to backups/ (gitignored). Credentials come from inside
# the running postgres container, so this works with any .env.
backup:
	@mkdir -p backups
	docker compose exec -T postgres sh -c 'pg_dump -U "$$POSTGRES_USER" "$$POSTGRES_DB"' > backups/events_$$(date +%F_%H%M%S).sql
	@echo "Backup written: backups/$$(ls -1t backups | head -1)"

# Rebuild images and restart WITHOUT touching data. (This used to go through
# clean and wipe the database; use 'make clean CONFIRM=yes' for a real reset.)
re: down up
	@echo "Rebuild complete."

# Destructive targets refuse to run without CONFIRM=yes.
define confirm_or_die
@if [ "$(CONFIRM)" != "yes" ]; then \
	echo "Refusing: 'make $(1)' $(2)"; \
	echo "Re-run as: make $(1) CONFIRM=yes"; \
	exit 1; \
fi
endef

clean:
	$(call confirm_or_die,clean,deletes the database volume.)
	docker compose down -v
	@echo "Containers and volumes removed"

fclean:
	$(call confirm_or_die,fclean,deletes the database volume and this project's images.)
	docker compose down -v --rmi all
	@echo "Containers, volumes, and project images removed"

prune:
	$(call confirm_or_die,prune,deletes the database volume.)
	docker volume rm events_postgres_data 2>/dev/null || true
	docker network prune -f
	@echo "Volume and networks removed"

help:
	@echo "Available commands:"
	@echo "  make build   - Build Docker images"
	@echo "  make up      - Build and run containers in background (no tunnel)"
	@echo "  make prod    - Build and run WITH the Cloudflare tunnel"
	@echo "  make down    - Stop containers (data kept)"
	@echo "  make logs    - Tail container logs"
	@echo "  make backup  - Dump the database to backups/"
	@echo "  make re      - Rebuild images and restart (data kept)"
	@echo "  make clean   - Remove containers and volumes    [needs CONFIRM=yes]"
	@echo "  make fclean  - Also remove this project's images [needs CONFIRM=yes]"
	@echo "  make prune   - Remove postgres volume/networks  [needs CONFIRM=yes]"

.PHONY: all build up prod down logs backup clean fclean prune re help
