# syntax=docker/dockerfile:1

FROM golang:1.25 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath -ldflags="-s -w" \
    -o /out/summarizer ./cmd/summarizer

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

COPY --from=build /out/summarizer /summarizer

ENV DB_PATH=/data/summarizer.db
VOLUME /data

ENTRYPOINT ["/summarizer"]
