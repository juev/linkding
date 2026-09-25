# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS frontend
WORKDIR /build
COPY package.json package-lock.json ./
RUN npm ci
COPY web/frontend ./web/frontend
COPY web/styles ./web/styles
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.27-bookworm AS go-build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/linkding ./cmd/linkding

FROM debian:bookworm-slim AS linkding
LABEL org.opencontainers.image.source="https://github.com/juev/linkding"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl media-types \
    && apt-get clean && rmdir /var/lib/apt/lists/partial && find /var/lib/apt/lists -maxdepth 1 -type f -delete
WORKDIR /etc/linkding
COPY --from=go-build /out/linkding /usr/local/bin/linkding
COPY web/static ./web/static
COPY --from=frontend /build/web/static/ ./web/static/
COPY LICENSE.txt ./LICENSE.txt
COPY docs/third-party ./docs/third-party
RUN mkdir -p data && chmod g+w . data
EXPOSE 9090
HEALTHCHECK --interval=30s --retries=3 --timeout=3s \
  CMD curl -fsS "http://127.0.0.1:${LD_SERVER_PORT:-9090}/${LD_CONTEXT_PATH}health" || exit 1
ENTRYPOINT ["/usr/local/bin/linkding"]
CMD ["server"]

FROM --platform=$BUILDPLATFORM node:22-alpine AS ublock-build
WORKDIR /build
RUN apk add --no-cache curl jq unzip
RUN set -eu; \
    url="$(curl -fsSL 'https://api.github.com/repos/uBlockOrigin/uBOL-home/releases?per_page=20' | jq -r 'first(.[] | select(.prerelease == false) | .assets[] | select(.name | endswith(".chromium.zip")) | .browser_download_url) // empty')"; \
    test -n "$url"; \
    curl -fL -o ublock.zip "$url"; \
    mkdir uBOLite.chromium.mv3; \
    unzip -q ublock.zip -d uBOLite.chromium.mv3; \
    jq '.declarative_net_request.rule_resources |= map(if .id == "annoyances-overlays" or .id == "annoyances-cookies" or .id == "annoyances-social" or .id == "annoyances-widgets" or .id == "annoyances-others" then .enabled = true else . end)' uBOLite.chromium.mv3/manifest.json > manifest.json; \
    mv manifest.json uBOLite.chromium.mv3/manifest.json

FROM node:22-bookworm-slim AS linkding-plus
LABEL org.opencontainers.image.source="https://github.com/juev/linkding"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates chromium curl media-types \
    && apt-get clean && rmdir /var/lib/apt/lists/partial && find /var/lib/apt/lists -maxdepth 1 -type f -delete
WORKDIR /etc/linkding
COPY --from=linkding /usr/local/bin/linkding /usr/local/bin/linkding
COPY --from=linkding /etc/linkding/web/static ./web/static
COPY --from=linkding /etc/linkding/LICENSE.txt ./LICENSE.txt
COPY --from=linkding /etc/linkding/docs/third-party ./docs/third-party
COPY --from=ublock-build /build/uBOLite.chromium.mv3 ./uBOLite.chromium.mv3
RUN npm install -g single-file-cli@2.0.75 \
    && npm install --prefix "$(npm root -g)/single-file-cli" simple-cdp@1.8.6 \
    && mkdir -p data chromium-profile \
    && chmod g+w . data chromium-profile uBOLite.chromium.mv3
ENV LD_ENABLE_SNAPSHOTS=True
EXPOSE 9090
HEALTHCHECK --interval=30s --retries=3 --timeout=3s \
  CMD curl -fsS "http://127.0.0.1:${LD_SERVER_PORT:-9090}/${LD_CONTEXT_PATH}health" || exit 1
ENTRYPOINT ["/usr/local/bin/linkding"]
CMD ["server"]
