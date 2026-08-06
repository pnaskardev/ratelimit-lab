BINARY     := tmp/main
PKG        := ./...
REDIS_PORT ?= 6379
REDIS_ADDR ?= localhost:$(REDIS_PORT)
APP_PORT   ?= 3000

export REDIS_PORT
export REDIS_ADDR
export APP_PORT

.DEFAULT_GOAL := up

## up: start redis if needed, then run the app with live reload
up: redis-up
	@if command -v air >/dev/null 2>&1; then air; else \
	  echo "air not found, falling back to 'go run .'"; go run .; fi

## run: start redis if needed, then run the app without live reload
run: redis-up
	go run .

## build: compile to tmp/main
build:
	go build -o $(BINARY) .

## down: stop redis
down:
	docker compose down

## help: list available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## //' | awk -F': ' '{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

# ---- tests ----

## test: run all tests
test:
	go test $(PKG)

## race: run all tests under the race detector
race: redis-up
	go test -race -count=1 $(PKG)

## bench: run benchmarks, no tests
bench: redis-up
	go test -run '^$$' -bench . -benchmem $(PKG)

## check: fmt, vet, then tests under -race
check: fmt vet race

## fmt: format all packages
fmt:
	go fmt $(PKG)

## vet: run go vet
vet:
	go vet $(PKG)

## tidy: sync go.mod/go.sum
tidy:
	go mod tidy

# ---- redis ----

# No-op when the container is already running, so it is cheap to depend on
# from every target that needs redis.
## redis-up: start redis only if it is not already running
redis-up:
	@if [ -n "$$(docker compose ps --status running --quiet redis 2>/dev/null)" ]; then \
	  echo "redis already running on port $(REDIS_PORT)"; \
	else \
	  echo "starting redis on port $(REDIS_PORT)..."; \
	  docker compose up -d --wait redis; \
	fi

## redis-cli: open a redis-cli shell
redis-cli: redis-up
	docker compose exec redis redis-cli

## redis-logs: tail redis logs
redis-logs:
	docker compose logs -f redis

## redis-flush: drop all keys (limiter state reset)
redis-flush: redis-up
	docker compose exec redis redis-cli FLUSHALL

## clean: stop redis and remove build artifacts
clean: down
	rm -rf tmp/ build-errors.log

.PHONY: up run build down help test race bench check fmt vet tidy \
        redis-up redis-cli redis-logs redis-flush clean
