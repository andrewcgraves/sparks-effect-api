FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/sparks-effect-api ./cmd/api

FROM alpine:3.20

RUN adduser -D -u 10001 appuser
COPY --from=build /out/sparks-effect-api /usr/local/bin/sparks-effect-api

USER appuser
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/sparks-effect-api"]
