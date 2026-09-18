.PHONY: test test-race vet build

test:
	go test -count=1 ./...

test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

build:
	go build ./cmd/ruleraven
