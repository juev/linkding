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
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/linkding ./cmd/linkding \
    && mkdir -p /out/runtime/etc/linkding/data \
    && chmod 1777 /out/runtime/etc/linkding/data

FROM --platform=$BUILDPLATFORM debian:bookworm-slim AS mime-data
RUN apt-get update && apt-get install -y --no-install-recommends media-types \
    && cp /etc/mime.types /mime.types

FROM gcr.io/distroless/static-debian13:nonroot AS linkding
LABEL org.opencontainers.image.source="https://github.com/juev/linkding"
COPY --from=go-build /out/runtime/ /
WORKDIR /etc/linkding
COPY --from=mime-data /mime.types /etc/mime.types
COPY --from=go-build /out/linkding /usr/local/bin/linkding
COPY web/static ./web/static
COPY --from=frontend /build/web/static/ ./web/static/
COPY LICENSE.txt ./LICENSE.txt
COPY docs/third-party ./docs/third-party
EXPOSE 9090
HEALTHCHECK --interval=30s --retries=3 --timeout=3s \
  CMD ["/usr/local/bin/linkding", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/linkding"]
CMD ["server"]

FROM --platform=$BUILDPLATFORM node:22-alpine AS ublock-build
WORKDIR /build
ARG UBLOCK_VERSION=2026.920.1710
ARG UBLOCK_SHA256=3ebf1458078d8738daf580e5ddeb41412cfa20fe4874a2fb321373f5ff7a09f1
RUN apk add --no-cache curl jq unzip
RUN set -eu; \
    curl -fL -o ublock.zip "https://github.com/uBlockOrigin/uBOL-home/releases/download/${UBLOCK_VERSION}/uBOLite_${UBLOCK_VERSION}.chromium.zip"; \
    printf '%s  %s\n' "$UBLOCK_SHA256" ublock.zip | sha256sum -c -; \
    mkdir uBOLite.chromium.mv3; \
    unzip -q ublock.zip -d uBOLite.chromium.mv3; \
    jq '.declarative_net_request.rule_resources |= map(if .id == "annoyances-overlays" or .id == "annoyances-cookies" or .id == "annoyances-social" or .id == "annoyances-widgets" or .id == "annoyances-others" then .enabled = true else . end)' uBOLite.chromium.mv3/manifest.json > manifest.json; \
    mv manifest.json uBOLite.chromium.mv3/manifest.json

FROM node:22-bookworm-slim AS linkding-plus
LABEL org.opencontainers.image.source="https://github.com/juev/linkding"
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates chromium media-types \
    && apt-get clean && rmdir /var/lib/apt/lists/partial && find /var/lib/apt/lists -maxdepth 1 -type f -delete
WORKDIR /etc/linkding
COPY --from=linkding /usr/local/bin/linkding /usr/local/bin/linkding
COPY --from=linkding /etc/linkding/web/static ./web/static
COPY --from=linkding /etc/linkding/LICENSE.txt ./LICENSE.txt
COPY --from=linkding /etc/linkding/docs/third-party ./docs/third-party
COPY --from=ublock-build /build/uBOLite.chromium.mv3 ./uBOLite.chromium.mv3
RUN npm install -g single-file-cli@2.0.75 \
    && npm install --prefix "$(npm root -g)/single-file-cli" simple-cdp@1.8.6 \
    && cli_browser="$(npm root -g)/single-file-cli/lib/browser.js" \
    && grep -q 'args.push("--single-process");' "$cli_browser" \
    && sed -i '/args.push("--single-process");/d' "$cli_browser" \
    && ! grep -q 'args.push("--single-process");' "$cli_browser" \
    && mkdir -p data \
    && chmod 1777 data
ENV LD_ENABLE_SNAPSHOTS=True HOME=/tmp XDG_CONFIG_HOME=/tmp/.chromium XDG_CACHE_HOME=/tmp/.chromium \
    LD_SINGLEFILE_UBLOCK_OPTIONS="'--browser-arg=\"--headless=new\"' '--browser-arg=\"--user-data-dir=./data/chromium-profile\"' '--browser-arg=\"--no-sandbox\"' '--browser-arg=\"--disable-dev-shm-usage\"' '--browser-arg=\"--disable-gpu\"'" \
    LD_SINGLEFILE_OPTIONS="--browser-wait-until=load --browser-wait-until-fallback=false"
USER 65532:65532
EXPOSE 9090
HEALTHCHECK --interval=30s --retries=3 --timeout=3s \
  CMD ["/usr/local/bin/linkding", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/linkding"]
CMD ["server"]
