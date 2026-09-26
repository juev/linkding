#!/usr/bin/env python3
"""Check a GoReleaser build before its artifacts are published."""

import hashlib
import json
from pathlib import Path
import sys
import tarfile
import zipfile


REQUIRED_FILES = {
    "LICENSE.txt",
    "README.md",
    "docs/installation.md",
    "docs/backups.md",
    "docs/migration.md",
    "docs/third-party/README.md",
    "docs/third-party/django-LICENSE",
    "web/static/bundle.js",
    "web/static/theme-light.css",
    "web/static/theme-dark.css",
    "web/static/admin/css/base.css",
}


def verify(directory: Path) -> None:
    tag = json.loads((directory / "metadata.json").read_text())["tag"]
    archives = {
        directory / f"linkding_{tag}_{os_name}_{arch}.{extension}"
        for os_name, extension in (("linux", "tar.gz"), ("darwin", "tar.gz"), ("windows", "zip"))
        for arch in ("amd64", "arm64")
    }
    checksum_lines = (directory / "SHA256SUMS").read_text().splitlines()
    checksums = {}
    for line in checksum_lines:
        digest, separator, name = line.partition("  ")
        if not separator or len(digest) != 64 or name in checksums:
            raise ValueError(f"invalid checksum line: {line}")
        checksums[name] = digest
    if set(checksums) != {path.name for path in archives}:
        raise ValueError("SHA256SUMS does not list exactly the six release archives")

    for archive in sorted(archives):
        digest = hashlib.sha256(archive.read_bytes()).hexdigest()
        if digest != checksums[archive.name]:
            raise ValueError(f"checksum mismatch: {archive.name}")
        if archive.suffix == ".zip":
            with zipfile.ZipFile(archive) as contents:
                names = contents.namelist()
        else:
            with tarfile.open(archive, "r:gz") as contents:
                names = contents.getnames()
        prefix = archive.name.removesuffix(".tar.gz").removesuffix(".zip") + "/"
        if not all(name.startswith(prefix) for name in names):
            raise ValueError(f"files outside release directory: {archive.name}")
        relative = {name.removeprefix(prefix) for name in names}
        required = REQUIRED_FILES | {"linkding.exe" if "_windows_" in archive.name else "linkding"}
        missing = required - relative
        if missing:
            raise ValueError(f"missing files in {archive.name}: {', '.join(sorted(missing))}")
        print(f"{archive.name}: {len(relative)} files, checksum verified")


if __name__ == "__main__":
    try:
        verify(Path(sys.argv[1] if len(sys.argv) == 2 else "dist"))
    except (OSError, KeyError, ValueError, tarfile.TarError, zipfile.BadZipFile) as error:
        sys.exit(str(error))
