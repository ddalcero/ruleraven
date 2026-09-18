# syntax=docker/dockerfile:1.7

FROM golang:1.22.12-bookworm@sha256:3d699e4d15d0f8f13c9195c0632a16702b8cbdece2955af1c23b37ae5d55a253 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY cmd ./cmd
COPY internal ./internal
ARG VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -buildvcs=false \
      -ldflags="-s -w -buildid= -X main.version=${VERSION}" \
      -o /out/ruleraven ./cmd/ruleraven

FROM gcr.io/distroless/static-debian12@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VERSION=dev
ARG VCS_REF=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="RuleRaven" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${VCS_REF}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://github.com/ddalcero/ruleraven"
COPY --from=build --chown=65532:65532 /out/ruleraven /ruleraven
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/ruleraven"]
CMD ["--config", "/etc/ruleraven/config.yaml"]
