.PHONY: all build build-frontend build-backend clean run dev test test-backend test-all lint help install-tools

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

# Default target
all: build

##@ Build

# Build target: all, backend, frontend. Example: make build TARGET=backend
TARGET ?= all

build: build-$(TARGET)  ## Build both backend and frontend by default, or specify TARGET=backend/frontend
	@echo "✅ Build completed!"

build-backend:  ## Build Go backend binary
	@echo "🔨 Building backend..."
	@mkdir -p $(BIN_DIR)
	$(GO_BUILD) -o $(SERVER_BIN) ./cmd/server/
	@echo "✅ Backend built: $(SERVER_BIN)"

build-frontend:  ## Build Vue frontend
	@echo "🔨 Building frontend..."
	@cd $(FRONTEND_DIR) && $(NPM_CMD) run build
	@echo "✅ Frontend built: $(FRONTEND_DIR)/dist"

build-all: build-backend build-frontend  ## Build both backend and frontend

##@ Development

run: build  ## Build and run the server
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

db-reset:  ## Reset database (delete and reinitialize)
	@echo "🗑️ Resetting database..."
	@rm -f $(DB_FILE)
	@echo "✅ Database reset"

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
	@awk 'BEGIN {FS = ":.*##"; printf "\n\033[1mOmniBot - 全平台智能助手\033[0m\n\n\033[36mUsage:\033[0m\n  make \033[32m<target>\033[0m\n\n  \033[36mExamples:\033[0m\n    make build                  # Build both\n    make build TARGET=backend   # Build backend only\n    make build TARGET=frontend  # Build frontend only\n\n"} /^[a-zA-Z_-]+:.*?##/ { printf "  \033[32m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

# Display help by default
.DEFAULT_GOAL := help
