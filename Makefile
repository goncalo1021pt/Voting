all: build

build:
	DOCKER_BUILDKIT=0 docker-compose build

run: build
	docker-compose up -d
	@echo "Containers running. Access backend at http://localhost:8080"

stop:
	docker-compose down

clean:
	docker-compose down -v
	@echo "Containers and volumes removed"

fclean: clean
	docker rm -f $$(docker ps -aq) 2>/dev/null || true
	docker rmi -f $$(docker images -q) 2>/dev/null || true
	@echo "All containers and images removed"

prune:
	docker volume rm voting_postgres_data 2>/dev/null || true
	docker network prune -f
	@echo "Volume and networks removed"

re: clean all
	docker-compose up -d
	@echo "Rebuild complete. Containers running."

help:
	@echo "Voting System - Available commands:"
	@echo "  make all    - Build containers (default)"
	@echo "  make build  - Build Docker images"
	@echo "  make run    - Build and run containers in background"
	@echo "  make stop   - Stop containers"
	@echo "  make clean  - Stop containers and remove volumes"
	@echo "  make fclean - Stop containers, remove volumes and delete images"
	@echo "  make prune  - Delete the postgres volume"
	@echo "  make re     - Clean, rebuild, and run"

.PHONY: all build run stop clean fclean prune re