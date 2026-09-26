#!/usr/bin/env bash
set -euo pipefail

tag=$(python3 -c 'import json; from pathlib import Path; print(json.loads(Path("dist/metadata.json").read_text())["tag"])')
smoke_dir=$(mktemp -d /tmp/linkding-release-smoke.XXXXXX)
run_id=${smoke_dir##*.}
name=
volume=
cleanup() {
  if [ -n "$name" ] && docker inspect "$name" >/dev/null 2>&1; then
    docker stop "$name" >/dev/null || true
  fi
  if [ -n "$volume" ] && docker volume inspect "$volume" >/dev/null 2>&1; then
    docker volume rm "$volume" >/dev/null || true
  fi
  for file in "$smoke_dir"/*; do
    if [ -f "$file" ]; then unlink "$file"; fi
  done
  rmdir "$smoke_dir"
}
trap cleanup EXIT

for variant in linkding linkding-plus; do
  for arch in amd64 arm64; do
    name="linkding-release-smoke-${run_id}-${variant}-${arch}"
    volume="linkding-release-smoke-data-${run_id}-${variant}-${arch}"
    docker volume create "$volume" >/dev/null
    docker run --rm -d --name "$name" --platform "linux/$arch" \
      -p 127.0.0.1::9090 --read-only --user 10001:10001 \
      --tmpfs /tmp:rw,nosuid,nodev,size=512m \
      -v "$volume:/etc/linkding/data" \
      -e LD_CONTEXT_PATH=linkding/ \
      -e LD_SUPERUSER_NAME=admin \
      -e LD_SUPERUSER_PASSWORD=smoke-pass \
      "ghcr.io/juev/${variant}:${tag}-${arch}" >/dev/null
    port=$(docker port "$name" 9090/tcp | sed -n 's/.*://p')
    ready=0
    for attempt in $(seq 1 60); do
      if curl -fsS "http://127.0.0.1:${port}/linkding/health" >/dev/null 2>&1; then
        ready=1
        break
      fi
      sleep 2
    done
    if [ "$ready" -ne 1 ]; then
      docker logs "$name" --tail 50
      exit 1
    fi
    curl -fsS -o /dev/null "http://127.0.0.1:${port}/linkding/static/bundle.js"
    docker exec "$name" /usr/local/bin/linkding healthcheck
    docker cp "$name:/etc/linkding/data/secretkey.txt" "$smoke_dir/${name}-secret-before.txt"
    if [ "$variant" = linkding-plus ]; then
      if ! docker exec "$name" sh -c 'timeout 600 single-file \
        --browser-arg="--headless=new" \
        --browser-arg="--user-data-dir=./data/chromium-profile" \
        --browser-arg="--no-sandbox" \
        --browser-arg="--disable-dev-shm-usage" \
        --browser-arg="--load-extension=uBOLite.chromium.mv3" \
        http://127.0.0.1:9090/linkding/login/ /tmp/smoke.html'; then
        docker logs "$name" --tail 50
        exit 1
      fi
      docker exec "$name" sh -c "grep -q '<title>Login - Linkding' /tmp/smoke.html"
    fi
    docker restart "$name" >/dev/null
    docker cp "$name:/etc/linkding/data/secretkey.txt" "$smoke_dir/${name}-secret-after.txt"
    cmp "$smoke_dir/${name}-secret-before.txt" "$smoke_dir/${name}-secret-after.txt"
    printf '%s/%s: runtime and restart passed\n' "$variant" "$arch"
    docker stop "$name" >/dev/null
    docker volume rm "$volume" >/dev/null
    name=
    volume=
  done
done
