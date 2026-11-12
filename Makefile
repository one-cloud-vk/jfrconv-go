.PHONY: build test check

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o bin/jfrconv ./cmd/jfrconv

test:
	go test ./...
	python3 -m unittest discover -s tools -p 'test_*.py'

check:
	test -z "$$(gofmt -l cmd internal assets)"
	go vet ./...
	$(MAKE) test
