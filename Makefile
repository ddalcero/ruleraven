.PHONY: check fmt-check test test-race vet integration helm-test docs docker-build build

check: fmt-check test test-race vet docs

fmt-check:
	@files="$$(gofmt -l $$(git ls-files '*.go'))"; \
	if [ -n "$$files" ]; then \
		printf 'gofmt required for:\n%s\n' "$$files"; \
		exit 1; \
	fi

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

integration:
	go test -tags=integration -count=1 -timeout=10m ./test/integration

helm-test:
	helm lint --strict deploy/helm/ruleraven
	helm template ruleraven deploy/helm/ruleraven --namespace ruleraven-test >/dev/null
	python3 deploy/helm/ruleraven/tests/chart_assertions.py

docs:
	npx --yes markdownlint-cli2@0.18.1 README.md CONTRIBUTING.md SECURITY.md CHANGELOG.md 'docs/**/*.md'

docker-build:
	docker build --file deploy/docker/Dockerfile --tag ruleraven:local .

build:
	go build ./cmd/ruleraven
