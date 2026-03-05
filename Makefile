.PHONY: build test clean

build:
	docker run --rm -v "$(PWD)":/src -w /src golang:1.24-bookworm go build -buildvcs=false -o daedalus .

test:
	docker run --rm -v "$(PWD)":/src -w /src golang:1.24-bookworm go test -v ./...

clean:
	rm -f daedalus
