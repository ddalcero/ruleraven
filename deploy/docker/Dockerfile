# syntax=docker/dockerfile:1.7

FROM golang:1.22.2-bookworm@sha256:d0902bacefdde1cf45528c098d14e55d78c107def8a22d148eabd71582d7a99f AS build
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

FROM gcr.io/distroless/static-debian12@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2
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
