# Go serving plane. Build context = repo root.
FROM golang:1.25-alpine AS build

WORKDIR /src

# Copy manifests first so `go mod download` caches across source-only changes.
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download

COPY apps/api/ ./

# Static binary: distroless/scratch has no libc to link against.
# Trimpath + no symbol table keeps the image small and the build reproducible.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w" \
      -o /out/server ./cmd/server

# Distroless: no shell, no package manager — a smaller attack surface than alpine.
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/server /server

# Cloud Run injects PORT; the app defaults to 8080 when it is absent.
EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/server"]
