all: build

build:
	docker compose build

run: build
	docker compose up -d
	@echo "Containers running. Access backend at http://localhost:8080"

stop:
	docker compose down

logs:
	docker compose logs -f

clean:
	docker compose down -v
	@echo "Containers and volumes removed"

fclean: clean
	docker rmi -f $$(docker images -q) 2>/dev/null || true
	@echo "All containers and images removed"

prune:
	docker volume rm voting_postgres_data 2>/dev/null || true
	docker network prune -f
	@echo "Volume and networks removed"

re: clean all
	docker compose up -d
	@echo "Rebuild complete. Containers running."

help:
	@echo "Available commands:"
	@echo "  make build  - Build Docker images"
	@echo "  make run    - Build and run containers in background"
	@echo "  make stop   - Stop containers"
	@echo "  make logs   - Tail container logs"
	@echo "  make clean  - Stop containers and remove volumes"
	@echo "  make fclean - Remove containers, volumes, and images"
	@echo "  make prune  - Remove postgres volume and unused networks"
	@echo "  make re     - Clean, rebuild, and run"

.PHONY: all build run stop logs clean fclean prune re help
