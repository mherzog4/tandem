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
# Listen address and base URL come from env (PORT / TANDEM_ADDR /
# TANDEM_BASE_URL) so the platform can inject them — Railway sets PORT.
# No --addr flag here, or it would override PORT.
ENTRYPOINT ["/relay"]
