FROM golang:1.25-alpine AS builder
ARG VERSION=dev
ARG DEPLOY_DATE=unknown
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-X github.com/lizozom/whatsapp-nagger/internal/version.Version=${VERSION} -X github.com/lizozom/whatsapp-nagger/internal/version.DeployDate=${DEPLOY_DATE}" -o nagger ./cmd/nagger

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/nagger /usr/local/bin/nagger
CMD ["nagger"]
