.PHONY: all build server client run-server run-client clean docker-build docker-run docker-test

all: build

build: server client

server:
	go build -o bin/server ./cmd/server

client:
	go build -o bin/client ./cmd/client

run-server:
	go run ./cmd/server

run-client:
	go run ./cmd/client --file ./testdata/sample.wav

clean:
	rm -rf bin/*
	rm -rf output/*

docker-build:
	docker compose build

docker-run:
	docker compose up --build

docker-test:
	docker compose run client --file ./testdata/sample.wav

test:
	go test ./...

vet:
	go vet ./...

lint:
	go fmt ./...