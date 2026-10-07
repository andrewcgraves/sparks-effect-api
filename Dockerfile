FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
COPY contract ./contract
RUN go mod download

COPY . .
# The commit this image is built from, folded into every ETag (SPA-432). .git
# is not in the build context, so Go cannot stamp it itself. Left empty, the
# server falls back to a per-process tag: correct, just colder after restarts.
ARG BUILD_SHA=""
RUN CGO_ENABLED=0 go build \
	-ldflags "-X github.com/andrewcgraves/sparks-effect-api/internal/config.buildSHA=${BUILD_SHA}" \
	-o /out/sparks-effect-api ./cmd/api

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab

COPY --from=build --chown=nonroot:nonroot /out/sparks-effect-api /sparks-effect-api

USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/sparks-effect-api"]
