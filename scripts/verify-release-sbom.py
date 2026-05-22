#!/usr/bin/env python3
"""Validate Vivero release SBOM structure and asset checksums."""
from __future__ import annotations

import argparse
import hashlib
import json
import pathlib
import sys
from typing import Any, NoReturn, cast

DEFAULT_ASSETS = [
    "checksums.txt",
    "vivero.rb",
    "vivero_darwin_amd64.tar.gz",
    "vivero_darwin_arm64.tar.gz",
    "vivero_linux_amd64.tar.gz",
    "vivero_linux_arm64.tar.gz",
]
ROOT_PACKAGE = "github.com/gianfrancopiana/vivero"
ROOT_ID = "SPDXRef-Package-vivero"


def fail(message: str, payload: object | None = None) -> NoReturn:
    print(message, file=sys.stderr)
    if payload is not None:
        print(json.dumps(payload, indent=2, sort_keys=True), file=sys.stderr)
    raise SystemExit(1)


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def checksum_value(file_entry: dict) -> str:
    for checksum in file_entry.get("checksums") or []:
        if checksum.get("algorithm") == "SHA256" and checksum.get("checksumValue"):
            return checksum["checksumValue"]
    return ""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--version", required=True, help="Release tag, e.g. v0.1.0")
    parser.add_argument("--sbom", required=True, type=pathlib.Path, help="SPDX JSON SBOM path")
    parser.add_argument("--dist", type=pathlib.Path, help="Directory containing release assets to checksum")
    parser.add_argument("--asset", action="append", default=[], help="Asset name to require; may be repeated")
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    if not args.sbom.is_file():
        fail(f"SBOM file not found: {args.sbom}")
    loaded: object
    try:
        loaded = json.loads(args.sbom.read_text())
    except json.JSONDecodeError as exc:
        fail(f"invalid SBOM JSON: {exc}")
    if not isinstance(loaded, dict):
        fail("SBOM JSON root must be an object", loaded)
    sbom: dict[str, Any] = cast(dict[str, Any], loaded)

    if sbom.get("spdxVersion") != "SPDX-2.3":
        fail("SBOM must use SPDX-2.3", sbom)
    if sbom.get("SPDXID") != "SPDXRef-DOCUMENT":
        fail("SBOM document SPDXID mismatch", sbom)
    if args.version not in sbom.get("name", ""):
        fail(f"SBOM name does not include {args.version}", sbom.get("name"))

    creation = sbom.get("creationInfo") or {}
    creators = creation.get("creators") or []
    if not creation.get("created") or not any("generate-release-sbom.sh" in creator for creator in creators):
        fail("SBOM creationInfo does not identify the release SBOM generator", creation)

    packages = sbom.get("packages") or []
    root_packages = [pkg for pkg in packages if pkg.get("SPDXID") == ROOT_ID]
    if len(root_packages) != 1:
        fail("SBOM must contain exactly one Vivero root package", root_packages)
    root = root_packages[0]
    if root.get("name") != ROOT_PACKAGE:
        fail("SBOM root package name mismatch", root)
    if root.get("versionInfo") != args.version:
        fail("SBOM root package version mismatch", root)
    if len(packages) < 2:
        fail("SBOM should include Go module dependencies", packages)

    relationships = sbom.get("relationships") or []
    if not any(
        rel.get("spdxElementId") == "SPDXRef-DOCUMENT"
        and rel.get("relationshipType") == "DESCRIBES"
        and rel.get("relatedSpdxElement") == ROOT_ID
        for rel in relationships
    ):
        fail("SBOM does not describe the Vivero root package", relationships)
    if not any(
        rel.get("spdxElementId") == ROOT_ID and rel.get("relationshipType") == "DEPENDS_ON"
        for rel in relationships
    ):
        fail("SBOM root package has no DEPENDS_ON relationships", relationships)

    required_assets = args.asset or DEFAULT_ASSETS
    files = {entry.get("fileName"): entry for entry in sbom.get("files") or []}
    missing = [asset for asset in required_assets if asset not in files]
    if missing:
        fail(f"SBOM missing release asset file entries: {', '.join(missing)}", sorted(files))

    for asset in required_assets:
        digest = checksum_value(files[asset])
        if not digest:
            fail(f"SBOM asset {asset} has no SHA256 checksum", files[asset])
        if args.dist:
            asset_path = args.dist / asset
            if not asset_path.is_file():
                fail(f"release asset not found for SBOM checksum verification: {asset_path}")
            actual = sha256(asset_path)
            if actual != digest:
                fail(f"SBOM checksum mismatch for {asset}: got {digest}, want {actual}", files[asset])

    print(f"sbom: ok ({args.sbom})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
