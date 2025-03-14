# Go parameters
GOCMD = go
GOBUILD = $(GOCMD) build
GOCLEAN = $(GOCMD) clean
GOTEST = $(GOCMD) test
GOGET = $(GOCMD) get

# Main target
all: clean build

# Build target
build:
	$(GOBUILD) -o status-checker ./cmd/status-checker/main.go

# Clean target
clean:
	$(GOCLEAN)
	rm -f status-checker

# Test target
test:
	$(GOTEST) -v ./...

# Get dependencies target
get:
	$(GOGET) -v ./...