# API gateway / reverse proxy. Build context = repo root.
FROM golang:1.25-alpine AS build

WORKDIR /src
COPY apps/gateway/go.mod ./
RUN go mod download
COPY apps/gateway/ ./

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w" \
      -o /out/gateway .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/gateway /gateway
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/gateway"]
