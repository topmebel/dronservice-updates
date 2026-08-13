# DronService

DronService is a Go service for managing MediaMTX, video devices and IP cameras on Linux ARM64 devices such as Raspberry Pi 5.

## Versions

Releases use semantic versions in the strict form `vMAJOR.MINOR.PATCH`. Build metadata is embedded at link time and is available through:

```text
dronservice --version
GET /api/version
```

Local builds report `dev`. A release build example:

```bash
version=v0.1.0
commit="$(git rev-parse HEAD)"
built_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
  -ldflags "-s -w \
  -X DronService/internal/buildinfo.Version=${version} \
  -X DronService/internal/buildinfo.Commit=${commit} \
  -X DronService/internal/buildinfo.BuiltAt=${built_at}" \
  -o dronservice-linux-arm64 .
```

## GitHub Releases

The release workflow runs for tags such as `v0.1.0`. It tests the project, builds the Linux ARM64 binary, creates SHA-256 checksums, signs the binary and publishes a GitHub Release.

The workflow requires the repository secret `RELEASE_SIGNING_PRIVATE_KEY`. The corresponding public key is committed as `deploy/dronservice-release.pub`; the private key must never be committed.

Configure the secret with GitHub CLI:

```bash
gh secret set RELEASE_SIGNING_PRIVATE_KEY < ~/.config/dronservice/release-signing-private.pem
```

Create a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Use a public GitHub repository for release assets. The source repository can remain private and publish binaries into a separate public updates repository if required.

## Manual updates

Application installation is never started by a timer. The UI checks the latest stable GitHub Release and shows `Обновить до vX.Y.Z` only when a newer signed release exists. Installation starts only after the operator presses the button and confirms the action.

Configure each device in `/etc/dronservice/update.conf`:

```text
DRONSERVICE_UPDATE_REPOSITORY=owner/repository
DRONSERVICE_UPDATE_PUBLIC_KEY=/usr/local/etc/dronservice-release.pub
```

The unprivileged DronService process writes `/var/lib/dronservice/update-dronservice.request`. The systemd path unit starts the root-owned oneshot updater, which:

1. downloads the binary, checksum and signature from the requested GitHub tag;
2. verifies SHA-256 and the RSA signature;
3. verifies that the signed binary reports the requested version;
4. installs it under `/usr/local/lib/dronservice/releases/vX.Y.Z`;
5. atomically changes the `current` symlink;
6. restarts DronService and checks `/api/health` and `/api/version`;
7. restores the previous release if validation or health checks fail.

There is deliberately no `dronservice-update.timer`.

## Bootstrap another Raspberry Pi

Copy the release binary and the `deploy` directory to the target once, then run:

```bash
sudo ./deploy/install-dronservice.sh ./dronservice-linux-arm64 owner/repository
```

The installer creates the release layout, installs the public verification key and systemd units, enables the manual update path, and verifies the local health endpoint. Device configuration remains under `/var/lib/dronservice` and is not included in release artifacts.

## Quality checks

```bash
gofmt -d .
go vet ./...
go test ./...
go test -race ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
```

## Dahua cameras

Dahua discovery uses the `Init` value from the DHIP `client.notifyDevInfo`
response. Explicit boolean or textual initialized/uninitialized values are
mapped to the application status. Missing and unknown vendor-specific values
remain `unknown`; authentication success is not used as an initialization
test.

For an initialized camera with saved server-side credentials, DronService can
read the main and sub-stream resolution/FPS from Dahua's `Encode`
configuration and can change the static IPv4 address, subnet mask and gateway.
The camera web interface is opened directly at its discovered IP address and
HTTP port.
