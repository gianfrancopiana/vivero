# Installing Vivero

Vivero release artifacts are published for macOS and Linux on `amd64` and `arm64`.

## One-line installer

```sh
curl -fsSL https://raw.githubusercontent.com/gianfrancopiana/vivero/main/scripts/install.sh | bash
```

Defaults:

- Version: latest GitHub release
- Install path: `~/.local/bin/vivero`
- Checksum: verified against the release `checksums.txt`

Pinned install:

```sh
curl -fsSL https://raw.githubusercontent.com/gianfrancopiana/vivero/v0.1.0/scripts/install.sh \
  | bash -s -- --version v0.1.0 --bin-dir /usr/local/bin
```

## Manual install

```sh
version=v0.1.0
os=darwin   # darwin or linux
arch=arm64  # arm64 or amd64
asset="vivero_${os}_${arch}.tar.gz"

curl -fsSLO "https://github.com/gianfrancopiana/vivero/releases/download/${version}/${asset}"
curl -fsSLO "https://github.com/gianfrancopiana/vivero/releases/download/${version}/checksums.txt"

grep "  ${asset}$" checksums.txt | sha256sum -c -
tar -xzf "${asset}"
install -m 0755 vivero ~/.local/bin/vivero
vivero version --json --no-input
```

On macOS, use `shasum -a 256` if `sha256sum` is unavailable.

## Homebrew formula asset

Each release uploads a generated `vivero.rb` formula asset with per-platform archive checksums.

```sh
version=v0.1.0
curl -fsSLO "https://github.com/gianfrancopiana/vivero/releases/download/${version}/vivero.rb"
brew install ./vivero.rb
```

This keeps Homebrew installable without requiring a separate tap repo. If a tap is created later, this formula is the generated source for it.

## Verify GitHub artifact attestations

Release archives, `checksums.txt`, and the generated Homebrew formula are attested by GitHub Actions.

```sh
gh attestation verify ./vivero_darwin_arm64.tar.gz --repo gianfrancopiana/vivero
gh attestation verify ./checksums.txt --repo gianfrancopiana/vivero
gh attestation verify ./vivero.rb --repo gianfrancopiana/vivero
```

Use the attestation check together with checksum verification: attestations prove the artifact came from the release workflow; checksums prove the downloaded bytes match the release manifest. Checksum verification protects against corrupt or mismatched downloaded artifacts, but it does not replace trusting the release manifest source or verifying the GitHub attestation.
