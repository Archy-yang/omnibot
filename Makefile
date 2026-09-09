.PHONY: all build build-frontend build-backend clean run dev start stop restart status logs test test-backend test-all lint help install-tools

# Go related variables
GO_CMD = go
GO_BUILD = $(GO_CMD) build
GO_TEST = $(GO_CMD) test
GO_LINT = golangci-lint run
GO_DIRS = ./internal/... ./cmd/...
BIN_DIR = bin
SERVER_BIN = $(BIN_DIR)/server

# Frontend related variables
FRONTEND_DIR = frontend
NPM_CMD = npm
VITE_CMD = npx vite

# Database
DB_FILE = omnibot.db

# 守护进程(CLAUDE.md 铁律:启动/重启必须走 make,禁止手动 kill / nohup)
CONFIG_PATH = configs/config.yaml
PID_FILE = $(BIN_DIR)/omnibot.pid
LOG_FILE = logs/omnibot.log

# Default target: 完整构建(前端+后端)并后台启动
all: build start

##@ Build

# Build target: all, backend, frontend. Example: make build TARGET=backend
TARGET ?= all

build: build-$(TARGET)  ## Build both backend and frontend by default, or specify TARGET=backend/frontend
	@echo "✅ Build completed!"

build-backend:  ## Build Go backend binary
	@echo "🔨 Building backend..."
	@mkdir -p $(BIN_DIR)
	CGO_ENABLED=0 $(GO_BUILD) -o $(SERVER_BIN) ./cmd/server/
	@echo "✅ Backend built: $(SERVER_BIN)"

build-frontend:  ## Build Vue frontend
	@echo "🎨 Building frontend..."
	@cd $(FRONTEND_DIR) && $(NPM_CMD) install && $(NPM_CMD) run build
	@echo "✅ Frontend built: $(FRONTEND_DIR)/dist"

build-all: build-backend build-frontend  ## Build both backend and frontend

##@ Service (daemon)

start: stop  ## Start the server in background (idempotent: stops old process first)
	@mkdir -p logs
	@nohup ./$(SERVER_BIN) -config $(CONFIG_PATH) >> $(LOG_FILE) 2>&1 & \
	echo "$$!" > $(PID_FILE)
	@sleep 1; if kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
		echo "✅ 已启动 PID=$$(cat $(PID_FILE))  日志: $(LOG_FILE)"; \
	else \
		echo "❌ 启动失败,最近日志:"; tail -20 $(LOG_FILE); exit 1; \
	fi

restart: stop start  ## Restart only (no rebuild)

stop:  ## Stop the running server (pidfile first, pgrep fallback)
	@pids=""; \
	[ -f $(PID_FILE) ] && pids=$$(cat $(PID_FILE)) 2>/dev/null; \
	for p in $$(pgrep -f "$(SERVER_BIN) -config" 2>/dev/null); do \
		[ "$$p" != "$$" ] && pids="$$pids $$p"; \
	done; \
	if [ -n "$$(echo $$pids | tr -d ' ')" ]; then \
		echo "🛑 停止进程:$$pids"; \
		kill $$pids 2>/dev/null || true; \
		for i in 1 2 3 4 5 6 7 8 9 10; do \
			alive=0; for p in $$pids; do kill -0 $$p 2>/dev/null && alive=1; done; \
			[ $$alive -eq 0 ] && break; sleep 0.5; \
		done; \
		for p in $$pids; do kill -0 $$p 2>/dev/null && kill -9 $$p 2>/dev/null || true; done; \
	else \
		echo "ℹ️ 无运行中的服务"; \
	fi; \
	rm -f $(PID_FILE)

status:  ## Show server status + recent log tail
	@if [ -f $(PID_FILE) ] && kill -0 $$(cat $(PID_FILE)) 2>/dev/null; then \
		echo "🟢 运行中 PID=$$(cat $(PID_FILE))"; \
	else \
		echo "🔴 未运行"; \
	fi
	@[ -f $(LOG_FILE) ] && tail -5 $(LOG_FILE) || true

logs:  ## Follow server log
	@tail -f $(LOG_FILE)

##@ Development

run: build  ## Build and run the server (foreground)
	@echo "🚀 Starting OmniBot server..."
	@./$(SERVER_BIN)

dev:  ## Run development mode (frontend dev server only)
	@echo "🎨 Starting frontend dev server..."
	@cd $(FRONTEND_DIR) && $(NPM_CMD) run dev

dev-backend:  ## Run backend dev server with hot reload (requires air)
	@echo "🔧 Starting backend dev server with hot reload..."
	@air

##@ Testing

test: test-backend  ## Run backend tests

test-backend:  ## Run backend unit tests
	@echo "🧪 Running backend tests..."
	$(GO_TEST) -v -cover $(GO_DIRS)

test-all: test-backend  ## Run all tests (currently backend only)

test-coverage:  ## Run tests with coverage report
	@echo "📊 Running tests with coverage..."
	$(GO_TEST) -coverprofile=coverage.out -covermode=atomic $(GO_DIRS)
	@$(GO_CMD) tool cover -html=coverage.out -o coverage.html
	@echo "✅ Coverage report generated: coverage.html"

##@ Code Quality

lint:  ## Run Go linter (requires golangci-lint)
	@echo "🔍 Running linter..."
	@$(GO_LINT) $(GO_DIRS)

fmt:  ## Format Go code
	@echo "🎨 Formatting Go code..."
	@$(GO_CMD) fmt $(GO_DIRS)

vet:  ## Run Go vet
	@echo "🔍 Running go vet..."
	@$(GO_CMD) vet $(GO_DIRS)

##@ Dependencies

deps: deps-backend deps-frontend  ## Install all dependencies

deps-backend:  ## Install Go dependencies
	@echo "📦 Installing Go dependencies..."
	@$(GO_CMD) mod download
	@echo "✅ Go dependencies installed"

deps-frontend:  ## Install frontend dependencies
	@echo "📦 Installing frontend dependencies..."
	@cd $(FRONTEND_DIR) && $(NPM_CMD) install
	@echo "✅ Frontend dependencies installed"

install-tools:  ## Install development tools (golangci-lint, air, etc.)
	@echo "🛠️ Installing development tools..."
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@go install github.com/cosmtrek/air@latest
	@echo "✅ Development tools installed"

##@ Database

db-up:  ## Start PostgreSQL + pgvector via Docker
	@echo "🐘 Starting PostgreSQL with pgvector..."
	@docker compose up -d postgres
	@echo "⏳ Waiting for PostgreSQL to be ready..."
	@sleep 5
	@echo "✅ PostgreSQL ready at localhost:5432"

db-up-all:  ## Start all services (PostgreSQL + Redis)
	@echo "🐘 Starting all services..."
	@docker compose up -d
	@sleep 5
	@echo "✅ All services ready"

db-down:  ## Stop Docker services
	@echo "🛑 Stopping Docker services..."
	@docker compose down
	@echo "✅ Services stopped"

db-logs:  ## Show PostgreSQL logs
	@docker compose logs -f postgres

db-reset:  ## Reset database (delete and reinitialize - SQLite only)
	@echo "🗑️ Resetting SQLite database..."
	@rm -f $(DB_FILE)
	@echo "✅ SQLite database reset"

db-purge:  ## Purge Docker volumes (WARNING: all data lost!)
	@echo "⚠️  Purging Docker volumes - ALL DATA WILL BE LOST!"
	@docker compose down -v
	@echo "✅ Docker volumes purged"

##@ Testing Database

test-postgres:  ## Run PostgreSQL + pgvector integration tests with Testcontainers
	@echo "🧪 Running PostgreSQL integration tests with Testcontainers..."
	@CGO_ENABLED=0 $(GO_TEST) -tags=postgres_integration -v $(GO_DIRS)
	@echo "✅ PostgreSQL integration tests completed"

##@ Cleanup

clean:  ## Clean build artifacts and temporary files
	@echo "🧹 Cleaning up..."
	@rm -rf $(BIN_DIR)
	@rm -rf $(FRONTEND_DIR)/dist
	@rm -f coverage.out coverage.html
	@rm -f $(DB_FILE)
	@echo "✅ Cleanup completed"

##@ Help

help:  ## Display this help message
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1mOmniBot - 全平台智能助手\033[0m\n\n\033[36mUsage:\033[0m\n  make \033[32m<target>\033[0m\n\n  \033[36mExamples:\033[0m\n    make                        # Build & start (daemon)\n    make restart                # Restart without rebuild\n    make build TARGET=backend   # Build backend only\n\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[32m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.DEFAULT_GOAL := all
