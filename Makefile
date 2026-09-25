GO ?= go

GROVE_BINARY := bin/grove
DEMO_BASE_BINARY := bin/groveshop-base
DEMO_BINARY := bin/groveshop
DEMO_CONFIG := configs/acme.yaml
GROVESHOP_REF ?= main

.PHONY: build-demo
build-demo:
	@mkdir -p $(dir $(DEMO_BINARY))
	$(GO) build -o $(GROVE_BINARY) ./cmd/grove
	GOBIN=$(CURDIR)/$(dir $(DEMO_BASE_BINARY)) $(GO) install github.com/grove-project/groveshop/cmd/groveshop@$(GROVESHOP_REF)
	mv $(dir $(DEMO_BASE_BINARY))groveshop $(DEMO_BASE_BINARY)
	@output="$(DEMO_BINARY).tmp.$$$$"; \
		trap 'rm -f "$$output"' EXIT; \
		$(GROVE_BINARY) config embed \
			--binary $(DEMO_BASE_BINARY) \
			--config $(DEMO_CONFIG) \
			--output "$$output"; \
		mv "$$output" $(DEMO_BINARY)
