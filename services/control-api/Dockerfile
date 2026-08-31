# syntax=docker/dockerfile:1

FROM golang:1.21 AS build

WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/control-api \
    ./services/control-api/cmd/control-api

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/control-api /control-api
ENTRYPOINT ["/control-api"]
