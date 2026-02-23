# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/diffbreak ./

FROM gcr.io/distroless/base-debian12

WORKDIR /
COPY --from=builder /out/diffbreak /diffbreak

EXPOSE 8080
USER nonroot:nonroot

ENTRYPOINT ["/diffbreak"]
