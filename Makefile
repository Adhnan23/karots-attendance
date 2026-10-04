BIN     := attendance
PKG     := ./cmd/attendance
DB      ?= attendance.db
ADDR    ?= :8080
TZ      ?= Asia/Colombo
# Pure Go (no cgo): one fully static binary with templates, CSS, JS and migrations embedded.
GOFLAGS := CGO_ENABLED=0
LDFLAGS := -s -w

.PHONY: help build run dev test vet fmt admin backup release clean up down logs docker-admin docker-backup

help: ## show targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-14s %s\n", $$1, $$2}'

build: ## build ./attendance for this machine
	$(GOFLAGS) go build -trimpath -ldflags="$(LDFLAGS)" -o $(BIN) $(PKG)

run: build ## build and run locally (Colombo time)
	TZ=$(TZ) ./$(BIN) -addr $(ADDR) -db $(DB)

dev: ## run without building a binary
	TZ=$(TZ) go run $(PKG) -addr $(ADDR) -db $(DB)

test: vet ## vet + all tests
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal

admin: build ## create/reset an admin: make admin PHONE=07xxxxxxxx PASS='secret123' [NAME=Owner]
	@test -n "$(PHONE)" -a -n "$(PASS)" || (echo "usage: make admin PHONE=07xxxxxxxx PASS='secret123' [NAME=Owner]"; exit 1)
	./$(BIN) -db $(DB) admin $(PHONE) '$(PASS)' $(or $(NAME),Owner)

backup: ## consistent copy of $(DB) into backups/ (safe while running)
	@mkdir -p backups
	./$(BIN) -db $(DB) backup backups/attendance-$$(date +%Y%m%d-%H%M%S).db

release: test ## static linux binaries in dist/ (amd64 + arm64) for the VPS
	mkdir -p dist
	$(GOFLAGS) GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BIN)-linux-amd64 $(PKG)
	$(GOFLAGS) GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="$(LDFLAGS)" -o dist/$(BIN)-linux-arm64 $(PKG)
	@ls -lh dist

clean:
	rm -rf $(BIN) dist

# ---- Docker (data lives in ./data) ----
up: ## docker: build and start in the background
	@mkdir -p data
	docker compose up -d --build

down: ## docker: stop
	docker compose down

logs: ## docker: follow logs
	docker compose logs -f

docker-admin: ## docker: create/reset an admin: make docker-admin PHONE=07xxxxxxxx PASS='secret123'
	@test -n "$(PHONE)" -a -n "$(PASS)" || (echo "usage: make docker-admin PHONE=07xxxxxxxx PASS='secret123' [NAME=Owner]"; exit 1)
	@mkdir -p data
	docker compose run --rm attendance admin $(PHONE) '$(PASS)' $(or $(NAME),Owner)

docker-backup: ## docker: consistent copy into data/backups/ while running
	@mkdir -p data/backups
	docker compose exec attendance /attendance -db /data/attendance.db backup /data/backups/attendance-$$(date +%Y%m%d-%H%M%S).db
