#!/usr/bin/env bash
set -euo pipefail

repo="gianfrancopiana/vivero"
module_path="github.com/gianfrancopiana/vivero"
dist_dir="dist"
output=""
version=""

usage() {
  cat <<'EOF'
Generate an SPDX JSON SBOM for a Vivero release.

Usage:
  scripts/generate-release-sbom.sh --version v0.1.0 [--dist dist] [--output dist/vivero_sbom.spdx.json]

The SBOM describes the Go module dependency graph and the release assets users
install: archives, checksums.txt, and the generated Homebrew formula.
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      version="${2:?missing version}"
      shift 2
      ;;
    --dist)
      dist_dir="${2:?missing dist dir}"
      shift 2
      ;;
    --output)
      output="${2:?missing output path}"
      shift 2
      ;;
    --repo)
      repo="${2:?missing repo}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

if [ -z "$version" ]; then
  echo "--version is required" >&2
  usage >&2
  exit 2
fi
if [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "version must look like vMAJOR.MINOR.PATCH, got: $version" >&2
  exit 2
fi
if [[ ! "$repo" =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
  echo "repo must look like OWNER/REPO, got: $repo" >&2
  exit 2
fi
if [ -z "$output" ]; then
  output="$dist_dir/vivero_sbom.spdx.json"
fi
if [ ! -d "$dist_dir" ]; then
  echo "dist directory not found: $dist_dir" >&2
  exit 1
fi

for cmd in go python3; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    echo "$cmd is required" >&2
    exit 127
  fi
done

required_assets=(
  checksums.txt
  vivero.rb
  vivero_darwin_amd64.tar.gz
  vivero_darwin_arm64.tar.gz
  vivero_linux_amd64.tar.gz
  vivero_linux_arm64.tar.gz
)
for asset in "${required_assets[@]}"; do
  if [ ! -f "$dist_dir/$asset" ]; then
    echo "missing release asset for SBOM: $asset" >&2
    exit 1
  fi
done

tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

GOFLAGS=-mod=readonly go list -m -json all > "$tmp/modules.json"
python3 - "$version" "$repo" "$module_path" "$dist_dir" "$output" "$tmp/modules.json" "${required_assets[@]}" <<'PY'
import datetime as dt
import hashlib
import json
import pathlib
import re
import sys
import urllib.parse
import uuid

version, repo, module_path, dist_dir, output, modules_file, *assets = sys.argv[1:]
dist = pathlib.Path(dist_dir)
out = pathlib.Path(output)

raw = pathlib.Path(modules_file).read_text()
decoder = json.JSONDecoder()
modules = []
idx = 0
while idx < len(raw):
    while idx < len(raw) and raw[idx].isspace():
        idx += 1
    if idx >= len(raw):
        break
    obj, idx = decoder.raw_decode(raw, idx)
    modules.append(obj)
if not modules:
    raise SystemExit("go list -m -json all returned no modules")

created = dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")

def spdx_id(prefix, value):
    cleaned = re.sub(r"[^A-Za-z0-9.-]+", "-", value).strip("-")
    if not cleaned:
        cleaned = uuid.uuid4().hex
    return f"SPDXRef-{prefix}-{cleaned}"

def purl_for(module):
    path = module.get("Path", "")
    ver = module.get("Version", "")
    if not path or not ver:
        return ""
    encoded_path = urllib.parse.quote(path, safe="/")
    encoded_version = urllib.parse.quote(ver, safe="")
    return f"pkg:golang/{encoded_path}@{encoded_version}"

root_id = "SPDXRef-Package-vivero"
packages = [
    {
        "name": module_path,
        "SPDXID": root_id,
        "versionInfo": version,
        "downloadLocation": f"https://github.com/{repo}/archive/refs/tags/{version}.tar.gz",
        "filesAnalyzed": False,
        "licenseConcluded": "NOASSERTION",
        "licenseDeclared": "NOASSERTION",
        "copyrightText": "NOASSERTION",
    }
]
relationships = [
    {
        "spdxElementId": "SPDXRef-DOCUMENT",
        "relationshipType": "DESCRIBES",
        "relatedSpdxElement": root_id,
    }
]
seen_ids = {root_id}
for module in modules:
    if module.get("Path") == module_path:
        continue
    path = module.get("Path") or "unknown"
    version_info = module.get("Version") or "NOASSERTION"
    package_id = spdx_id("Package", f"{path}-{version_info}")
    suffix = 2
    base_id = package_id
    while package_id in seen_ids:
        package_id = f"{base_id}-{suffix}"
        suffix += 1
    seen_ids.add(package_id)
    package = {
        "name": path,
        "SPDXID": package_id,
        "versionInfo": version_info,
        "downloadLocation": "NOASSERTION",
        "filesAnalyzed": False,
        "licenseConcluded": "NOASSERTION",
        "licenseDeclared": "NOASSERTION",
        "copyrightText": "NOASSERTION",
    }
    purl = purl_for(module)
    if purl:
        package["externalRefs"] = [
            {
                "referenceCategory": "PACKAGE-MANAGER",
                "referenceType": "purl",
                "referenceLocator": purl,
            }
        ]
    replace = module.get("Replace")
    if replace:
        package["supplier"] = "NOASSERTION"
        package["comment"] = f"module is replaced by {replace.get('Path', 'unknown')} {replace.get('Version', '')}".strip()
    packages.append(package)
    relationships.append(
        {
            "spdxElementId": root_id,
            "relationshipType": "DEPENDS_ON",
            "relatedSpdxElement": package_id,
        }
    )

files = []
seen_file_ids = set()
for asset in assets:
    path = dist / asset
    data = path.read_bytes()
    file_id = spdx_id("File", asset)
    suffix = 2
    base_id = file_id
    while file_id in seen_file_ids or file_id in seen_ids:
        file_id = f"{base_id}-{suffix}"
        suffix += 1
    seen_file_ids.add(file_id)
    files.append(
        {
            "fileName": asset,
            "SPDXID": file_id,
            "checksums": [
                {"algorithm": "SHA256", "checksumValue": hashlib.sha256(data).hexdigest()}
            ],
            "licenseConcluded": "NOASSERTION",
            "copyrightText": "NOASSERTION",
        }
    )
    relationships.append(
        {
            "spdxElementId": root_id,
            "relationshipType": "CONTAINS",
            "relatedSpdxElement": file_id,
        }
    )

document = {
    "spdxVersion": "SPDX-2.3",
    "dataLicense": "CC0-1.0",
    "SPDXID": "SPDXRef-DOCUMENT",
    "name": f"vivero {version} release SBOM",
    "documentNamespace": f"https://github.com/{repo}/sbom/{version}/{uuid.uuid4()}",
    "creationInfo": {
        "created": created,
        "creators": [
            "Tool: scripts/generate-release-sbom.sh",
            "Organization: gianfrancopiana/vivero maintainers",
        ],
    },
    "documentDescribes": [root_id],
    "packages": packages,
    "files": files,
    "relationships": relationships,
}
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(json.dumps(document, indent=2, sort_keys=True) + "\n")
print(out)
PY

scripts/verify-release-sbom.py --version "$version" --sbom "$output" --dist "$dist_dir" >/dev/null
