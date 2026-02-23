# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates git

ARG TARGETOS
ARG TARGETARCH
ENV GOOS=${TARGETOS:-linux}
ENV GOARCH=${TARGETARCH:-amd64}

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/diffbreak ./

FROM alpine:3.20

WORKDIR /

RUN apk add --no-cache ca-certificates

COPY --from=builder /out/diffbreak /diffbreak

EXPOSE 8080

ENTRYPOINT ["/diffbreak"]
