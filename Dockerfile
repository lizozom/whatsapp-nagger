# Stage 1: Build Go binary
FROM golang:1.25-alpine AS go-builder
ARG VERSION=dev
ARG DEPLOY_DATE=unknown
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-X github.com/lizozom/whatsapp-nagger/internal/version.Version=${VERSION} -X github.com/lizozom/whatsapp-nagger/internal/version.DeployDate=${DEPLOY_DATE}" -o nagger ./cmd/nagger

# Stage 2: Build Next.js dashboard (standalone output)
FROM node:22-alpine AS next-builder
WORKDIR /app/dashboard
COPY dashboard/package*.json ./
RUN npm ci
COPY dashboard/ .
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# Stage 3: Runtime — Node.js base (for Next.js) + Go binary
FROM node:22-alpine
RUN apk add --no-cache ca-certificates tzdata

# Go bot binary
COPY --from=go-builder /app/nagger /usr/local/bin/nagger

# Next.js standalone server + static assets
COPY --from=next-builder /app/dashboard/.next/standalone /app/dashboard
COPY --from=next-builder /app/dashboard/.next/static /app/dashboard/.next/static
COPY --from=next-builder /app/dashboard/public /app/dashboard/public

# Entrypoint runs both processes
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

CMD ["/entrypoint.sh"]
