.PHONY: docs build test vet

# Regenerate the OpenAPI spec from the swaggo annotations into docs/.
# Install the CLI once with: go install github.com/swaggo/swag/cmd/swag@v1.16.4
docs:
	swag init -g cmd/api/main.go -o docs --parseDependency --parseInternal

build: docs
	go build ./...

test:
	go test ./...

vet:
	go vet ./...
