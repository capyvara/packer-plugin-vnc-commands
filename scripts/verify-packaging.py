#!/usr/bin/env python3
import hashlib
import os
from pathlib import Path
import re
import sys
import zipfile

root = Path(sys.argv[1])
checksums = list(root.glob("packer-plugin-vnc-commands_v*_SHA256SUMS"))
assert len(checksums) == 1, "Expected one Packer checksum manifest"
sums = {}
for line in checksums[0].read_text().splitlines():
    digest, name = line.split(maxsplit=1)
    sums[name.lstrip("*")] = digest
seen = set()
api = os.environ["PACKER_PLUGIN_API_VERSION"]
for archive in root.glob("*.zip"):
    match = re.fullmatch(r"packer-plugin-vnc-commands_v(.+)_(x\d+\.\d+)_(linux|darwin|windows)_(amd64|arm64)\.zip", archive.name)
    assert match, f"Unexpected Packer archive name: {archive.name}"
    version, archive_api, system, arch = match.groups()
    assert archive_api == api, (archive_api, api)
    seen.add((system, arch))
    assert sums.get(archive.name) == hashlib.sha256(archive.read_bytes()).hexdigest(), archive.name
    binary = archive.stem + (".exe" if system == "windows" else "")
    with zipfile.ZipFile(archive) as z:
        assert z.testzip() is None, archive.name
        assert set(z.namelist()) == {binary, "LICENSE", "README.md"}, z.namelist()
        assert len(z.read(binary)) > 0
assert seen == {(system, arch) for system in ("linux", "darwin", "windows") for arch in ("amd64", "arm64")}, seen
assert set(sums) == {p.name for p in root.glob("*.zip")}, "Checksum manifest must cover exactly the ZIPs"
print("Verified six Packer ZIPs, binary names, and SHA256SUMS")
