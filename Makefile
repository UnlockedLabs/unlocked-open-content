# The image is named after this folder, which is the repo's name.
IMAGE := $(notdir $(CURDIR))

.PHONY: run worker migrate test lint build

# Open Content's routes live at the root (/api/...), like core's, so serve
# runs with no PATH_PREFIX.
run:
	go run ./cmd/app serve

worker:
	go run ./cmd/app worker

migrate:
	go run ./cmd/app migrate

test:
	go test -race ./...

# The same golangci-lint version CI runs, without installing it locally.
lint:
	docker run --rm -v $(CURDIR):/app -w /app golangci/golangci-lint:v2.14.0 golangci-lint run

build:
	docker build -t $(IMAGE) .
