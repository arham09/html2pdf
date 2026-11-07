GO ?= go

.PHONY: verify test build vet race fuzz bench

verify: build vet test

test:
	CGO_ENABLED=0 $(GO) test ./...

build:
	CGO_ENABLED=0 $(GO) build ./...

vet:
	$(GO) vet ./...

race:
	$(GO) test -race ./...

fuzz:
	$(GO) test . -run '^$$' -fuzz '^FuzzRender$$' -fuzztime=15s -parallel=2
	$(GO) test ./internal/pagination -run '^$$' -fuzz '^FuzzTableProgress$$' -fuzztime=15s -parallel=2
	$(GO) test ./internal/resource -run '^$$' -fuzz '^FuzzImageAndDataURL$$' -fuzztime=15s -parallel=2
	$(GO) test ./internal/font -run '^$$' -fuzz '^FuzzStaticFont$$' -fuzztime=15s -parallel=2
	$(GO) test ./internal/html -run '^$$' -fuzz '^FuzzParse$$' -fuzztime=15s -parallel=2
	$(GO) test ./internal/css -run '^$$' -fuzz '^FuzzParse$$' -fuzztime=15s -parallel=2

bench:
	CGO_ENABLED=0 $(GO) test -run '^$$' -bench BenchmarkRender -benchmem
