.PHONY: build build-linux run test tidy clean

build:
	go build -o bin/localops ./cmd/localops
	go build -o bin/localops-cli ./cmd/localops-cli

build-linux:
	GOOS=linux GOARCH=amd64 go build -o bin/localops ./cmd/localops
	GOOS=linux GOARCH=amd64 go build -o bin/localops-cli ./cmd/localops-cli

run: build
	./bin/localops

test:
	go test ./...

tidy:
	go mod tidy

clean:
	rm -rf bin localops.db*
