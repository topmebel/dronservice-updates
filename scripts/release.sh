#!/usr/bin/env bash
set -euo pipefail

version="${1:-}"
if ! printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "usage: $0 vMAJOR.MINOR.PATCH" >&2
  exit 2
fi

root="$(cd "$(dirname "$0")/.." && pwd)"
workdir="$(mktemp -d)"
key_file="${DRONSERVICE_RELEASE_SIGNING_KEY:-$HOME/.config/dronservice/release-signing-private.pem}"
cleanup() {
  rm -rf "$workdir"
}
trap cleanup EXIT

cd "$root"
gofmt -w .
go vet ./...
go test ./...

commit="$(git rev-parse HEAD)"
built_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w \
  -X DronService/internal/buildinfo.Version=${version} \
  -X DronService/internal/buildinfo.Commit=${commit} \
  -X DronService/internal/buildinfo.BuiltAt=${built_at}" \
  -o "$workdir/dronservice-linux-arm64" .

GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" \
  -o "$workdir/dronservice-camera-network-helper" ./cmd/camera-network-helper

for file in \
  deployment-manifest.json \
  install-dronservice.sh \
  update-dronservice.sh \
  install-mediamtx.sh \
  install-video-runtime.sh \
  dronservice.service \
  dronservice-update.service \
  dronservice-update.path \
  dronservice-mediamtx-install.service \
  dronservice-mediamtx-install.path \
  dronservice-camera-network.service \
  dronservice-camera-network.path \
  dronservice-camera-network.conf \
  mediamtx.service \
  mediamtx.yml \
  dronservice-release.pub
do
  cp "deploy/$file" "$workdir/$file"
done

(
  cd "$workdir"
  sha256sum \
    dronservice-linux-arm64 \
    dronservice-camera-network-helper \
    deployment-manifest.json \
    install-dronservice.sh \
    update-dronservice.sh \
    install-mediamtx.sh \
    install-video-runtime.sh \
    dronservice.service \
    dronservice-update.service \
    dronservice-update.path \
    dronservice-mediamtx-install.service \
    dronservice-mediamtx-install.path \
    dronservice-camera-network.service \
    dronservice-camera-network.path \
    dronservice-camera-network.conf \
    mediamtx.service \
    mediamtx.yml \
    dronservice-release.pub > checksums.sha256
)

test -r "$key_file"

if ! command -v gh >/dev/null 2>&1; then
  export PATH="/mnt/c/Program Files/GitHub CLI:$PATH"
fi
gh_cmd=""
if command -v gh >/dev/null 2>&1; then
  gh_cmd=gh
elif [ -x "/mnt/c/Program Files/GitHub CLI/gh.exe" ]; then
  gh_cmd="/mnt/c/Program Files/GitHub CLI/gh.exe"
fi
if [ -z "$gh_cmd" ]; then
  echo "gh CLI not found" >&2
  exit 127
fi

openssl dgst -sha256 -sign "$key_file" \
  -out "$workdir/dronservice-linux-arm64.sig" "$workdir/dronservice-linux-arm64"
openssl dgst -sha256 -sign "$key_file" \
  -out "$workdir/checksums.sha256.sig" "$workdir/checksums.sha256"

notes_file="$workdir/release-notes.md"
cat > "$notes_file" <<EOF
## DronService ${version}

- Страница MediaMTX переведена на карточный layout, как у камер.
- RTSP-пути, предпросмотр и редактирование стримов работают из карточек.
EOF

"$gh_cmd" release create "$version" \
  "$workdir"/dronservice-linux-arm64 \
  "$workdir"/dronservice-linux-arm64.sig \
  "$workdir"/dronservice-camera-network-helper \
  "$workdir"/checksums.sha256 \
  "$workdir"/checksums.sha256.sig \
  "$workdir"/deployment-manifest.json \
  "$workdir"/install-dronservice.sh \
  "$workdir"/update-dronservice.sh \
  "$workdir"/install-mediamtx.sh \
  "$workdir"/install-video-runtime.sh \
  "$workdir"/dronservice.service \
  "$workdir"/dronservice-update.service \
  "$workdir"/dronservice-update.path \
  "$workdir"/dronservice-mediamtx-install.service \
  "$workdir"/dronservice-mediamtx-install.path \
  "$workdir"/dronservice-camera-network.service \
  "$workdir"/dronservice-camera-network.path \
  "$workdir"/dronservice-camera-network.conf \
  "$workdir"/mediamtx.service \
  "$workdir"/mediamtx.yml \
  "$workdir"/dronservice-release.pub \
  --repo topmebel/dronservice-updates \
  --verify-tag \
  --title "$version" \
  --notes-file "$notes_file"

echo "Release ${version} published."
