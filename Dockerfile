FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /gse-exporter ./cmd/gse-exporter

# gse serves its leaf cert without the intermediate, and go does not chase the
# cert's AIA extension the way browsers do, so ship the issuer in the bundle
FROM alpine:3 AS certs
COPY certs/gse-issuer.pem /usr/local/share/ca-certificates/gse-issuer.crt
RUN apk add --no-cache ca-certificates && update-ca-certificates

FROM gcr.io/distroless/static:nonroot
COPY --from=certs /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /gse-exporter /gse-exporter
EXPOSE 9821
USER nonroot:nonroot
ENTRYPOINT ["/gse-exporter"]
