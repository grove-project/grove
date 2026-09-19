GO ?= go

DEMO_BINARY := bin/groveshop

.PHONY: build-demo
build-demo:
	@mkdir -p $(dir $(DEMO_BINARY))
	$(GO) build -o $(DEMO_BINARY) ./cmd/grovlet
