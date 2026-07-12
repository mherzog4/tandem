# Static relay build → distroless. Single binary, no CGO (NFR1).
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /relay ./cmd/relay

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /relay /relay
EXPOSE 8080
# BASE_URL is set by fly.toml env; the relay reads --base-url from it via
# the entrypoint below. Distroless has no shell, so pass flags directly.
ENTRYPOINT ["/relay", "--addr", ":8080"]
