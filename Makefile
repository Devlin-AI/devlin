.PHONY: all devlin gateway run-gateway run-devlin clean fmt vet

all: devlin gateway

devlin:
	go build -mod=mod -o devlin ./cmd/devlin

gateway:
	go build -mod=mod -o gateway ./cmd/gateway

run-gateway: gateway
	./gateway

run-devlin: devlin
	./devlin

clean:
	rm -f devlin gateway

fmt:
	gofmt -w .

vet:
	go vet -mod=mod ./...
