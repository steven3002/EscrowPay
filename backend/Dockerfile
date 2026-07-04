# syntax=docker/dockerfile:1

# Build a static Go binary. pgx is pure Go, so CGO is off and the result runs
# on a minimal distroless base with no libc.
FROM golang:1.25.7-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

# Runtime: distroless static includes CA certificates (needed for outbound
# HTTPS to the payment gateway) and runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/api /app/api

# Cloud Run injects PORT; the app honors it. EXPOSE is documentation only.
ENV PORT=8080
EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/app/api"]
