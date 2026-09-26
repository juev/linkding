#!/usr/bin/env python3
"""Package a cross-built linkding binary with its runtime files."""

import argparse
import gzip
import os
from pathlib import Path
import tarfile
import time
import zipfile


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--version", required=True)
    parser.add_argument("--os", required=True, choices=("linux", "darwin", "windows"))
    parser.add_argument("--arch", required=True, choices=("amd64", "arm64"))
    parser.add_argument("--binary", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()

    root = Path(__file__).resolve().parent.parent
    if not args.binary.is_file():
        parser.error(f"binary missing: {args.binary}")
    for generated in ("bundle.js", "theme-dark.css", "theme-light.css"):
        if not (root / "web/static" / generated).is_file():
            parser.error(f"frontend build missing: web/static/{generated}")

    executable = "linkding.exe" if args.os == "windows" else "linkding"
    files = [(args.binary, executable)]
    for name in ("LICENSE.txt", "README.md", "docs/installation.md", "docs/backups.md", "docs/migration.md"):
        files.append((root / name, name))
    for directory in ("web/static", "docs/third-party"):
        files.extend((path, str(path.relative_to(root))) for path in (root / directory).rglob("*") if path.is_file())
    files.sort(key=lambda item: item[1])
    for source, _ in files:
        if not source.is_file():
            parser.error(f"release file missing: {source}")

    timestamp = int(os.environ.get("SOURCE_DATE_EPOCH", str(int(time.time()))))
    timestamp = max(timestamp, 315532800)  # ZIP cannot represent dates before 1980.
    prefix = f"linkding_{args.version}_{args.os}_{args.arch}"
    args.output.mkdir(parents=True, exist_ok=True)
    suffix = ".zip" if args.os == "windows" else ".tar.gz"
    archive = args.output / f"{prefix}{suffix}"
    if args.os == "windows":
        with zipfile.ZipFile(archive, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as output:
            for source, relative in files:
                entry = zipfile.ZipInfo(f"{prefix}/{relative}", time.gmtime(timestamp)[:6])
                entry.compress_type = zipfile.ZIP_DEFLATED
                entry.external_attr = (0o755 if relative == executable else 0o644) << 16
                output.writestr(entry, source.read_bytes(), compress_type=zipfile.ZIP_DEFLATED, compresslevel=9)
    else:
        with archive.open("wb") as raw:
            with gzip.GzipFile(filename="", mode="wb", fileobj=raw, mtime=timestamp) as compressed:
                with tarfile.open(fileobj=compressed, mode="w") as output:
                    for source, relative in files:
                        info = output.gettarinfo(str(source), f"{prefix}/{relative}")
                        info.uid = info.gid = 0
                        info.uname = info.gname = ""
                        info.mtime = timestamp
                        info.mode = 0o755 if relative == executable else 0o644
                        with source.open("rb") as content:
                            output.addfile(info, content)
    print(archive)


if __name__ == "__main__":
    main()
