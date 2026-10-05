FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
COPY contract ./contract
RUN go mod download

COPY . .
# The commit being built, so error reports name the release that raised them.
# CI passes github.sha; production re-tags this same image, so it keeps it.
ARG RELEASE=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.release=${RELEASE}" -o /out/sparks-effect-api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build --chown=nonroot:nonroot /out/sparks-effect-api /sparks-effect-api

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/sparks-effect-api"]
