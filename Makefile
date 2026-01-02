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
	@echo "  make re     - Clean, rebuild, and run"

re: clean build

.PHONY: all build run stop clean re