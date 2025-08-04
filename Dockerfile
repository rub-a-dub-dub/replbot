FROM golang:1.23-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /replbot

FROM alpine:3.19

RUN apk add --no-cache tmux=3.3a_git20230428-r0 asciinema=2.4.0-r0 ttyd=1.7.4-r0

COPY --from=builder /replbot /usr/bin/replbot

ENTRYPOINT ["replbot"]
