version := `git describe --tags --always --dirty 2>/dev/null || echo dev`
chart := "charts/gse-exporter"

default:
    @just --list

build:
    go build -trimpath -ldflags "-s -w -X main.version={{version}}" -o bin/gse-exporter ./cmd/gse-exporter

test:
    go test -race ./...

vet:
    go vet ./...

fmt:
    gofmt -w .

run *args:
    go run ./cmd/gse-exporter -log.level debug {{args}}

docker:
    docker build --build-arg VERSION={{version}} -t gse-exporter:{{version}} .

lint-chart:
    helm lint {{chart}}
    helm template gse {{chart}} --set serviceMonitor.enabled=true \
      --set prometheusRule.enabled=true > /dev/null

check: vet test lint-chart

clean:
    rm -rf bin
