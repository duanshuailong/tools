# IsoFactory — common tasks. Run `make help` for the list.
.DEFAULT_GOAL := help

BACKEND  := backend
FRONTEND := frontend
TEMPLATE := ./template
ADDR     := :8080

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: backend
backend: ## Run the backend (go run) against ./template
	cd $(BACKEND) && go run ./cmd/isofactory -addr $(ADDR) -template ../template

.PHONY: frontend
frontend: ## Run the frontend dev server (proxies /api to :8080)
	cd $(FRONTEND) && npm run dev

.PHONY: test
test: ## Run backend tests with the race detector
	cd $(BACKEND) && go test -race ./...

.PHONY: vet
vet: ## go vet the backend
	cd $(BACKEND) && go vet ./...

.PHONY: build
build: ## Build production artifacts (backend binary + frontend dist)
	cd $(BACKEND) && go build -o isofactory ./cmd/isofactory
	cd $(FRONTEND) && npm run build

.PHONY: genpack
genpack: ## Generic build: SRC=<dir> OUT=<iso> [VOLUME=..] -> bootable/data ISO via xorriso
	@[ -n "$(SRC)" ] && [ -n "$(OUT)" ] || { echo "usage: make genpack SRC=<dir> OUT=<iso.iso> [VOLUME=LABEL]"; exit 2; }
	cd $(BACKEND) && go run ./cmd/isofactory genpack -src "$(SRC)" -out "$(OUT)" $(if $(VOLUME),-volume "$(VOLUME)",)

.PHONY: deps
deps: ## Install frontend dependencies
	cd $(FRONTEND) && npm install

.PHONY: clean
clean: ## Remove build artifacts and local data
	rm -f $(BACKEND)/isofactory
	rm -rf $(BACKEND)/data $(FRONTEND)/dist
