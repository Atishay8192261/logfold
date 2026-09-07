BINARY := praxis
DEMO   := examples/broken-web

.PHONY: build test race bench fmt vet run fix scan serve docker clean

build:
	go build -o $(BINARY) ./cmd/praxis

test:
	go test ./...

race:
	go test -race ./...

bench:
	go test -run=^$$ -bench=. -benchmem ./...

fmt:
	gofmt -w .

vet:
	go vet ./...

run: build
	./$(BINARY) run -C $(DEMO) "diagnose why the web service is unreachable on port 8080"

fix: build
	./$(BINARY) run -C $(DEMO) "fix the config so the gateway listens on port 8080"

scan: build
	./$(BINARY) scan -C $(DEMO)

serve: build
	./$(BINARY) serve -C $(DEMO)

docker:
	docker build -t praxis .

clean:
	rm -f $(BINARY)
