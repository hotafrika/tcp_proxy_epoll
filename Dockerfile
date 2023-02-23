# Build stage
FROM --platform=linux/amd64 golang:1.19 AS build

RUN apt install make gcc

WORKDIR /app
ADD . /app

RUN go mod download

RUN go build -o go-app cmd/main.go

FROM --platform=linux/amd64 golang:1.19
#FROM alpine:3.16.0

ENTRYPOINT ["/app/go-app", "-pprof", "-config", "_config.json", "-loglevel", "0"]
WORKDIR /app
#RUN apk --update --no-cache add ca-certificates tzdata && update-ca-certificates
RUN apt-get update -y
RUN apt install htop net-tools -y

COPY --from=build /app/go-app /app/go-app
COPY cmd/config.json /app/config.json
