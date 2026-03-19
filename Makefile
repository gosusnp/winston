
build:
	go build -ldflags '$(LDFLAGS)' -o winston ./cmd/winston

build-release:
	go build -ldflags '-s -w $(LDFLAGS)' -o winston ./cmd/winston

check: lint helm-lint helm-test test test-integration

ci: fmt-check license-check lint helm-lint helm-test test test-integration

clean:
	rm -f winston

fix: fmt license-fix

fmt:
	gofmt -w .

fmt-check:
	@if [ -n "$$(gofmt -l .)" ]; then \
		echo "Files need formatting (run 'make fmt'):"; \
		gofmt -l .; \
		exit 1; \
	fi

install:
	go install -ldflags '$(LDFLAGS)' ./cmd/winston

license-check:
	go tool addlicense -check -l mit -c "Jimmy Ma" -s=only .

license-fix:
	go tool addlicense -l mit -c "Jimmy Ma" -s=only .

helm-lint:
	helm lint helm/winston/

helm-test:
	helm unittest helm/winston/

lint:
	go vet $$(go list ./...)
	go tool golangci-lint run ./...

pre-commit: fix check

run:
	go run ./cmd/winston

test:
	go tool gotestsum --format pkgname-and-test-fails -- -cover -race $$(go list ./...)

test-integration:
	go tool gotestsum --format pkgname-and-test-fails -- -cover -race -tags integration ./internal/integration/...
