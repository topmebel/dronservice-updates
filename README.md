# DronService

DronService is a Go control service for MediaMTX, FFmpeg, V4L2 and IP cameras
on Raspberry Pi 5/Linux ARM64. It runs as the unprivileged `admin` user and is
intended only for a trusted LAN.

## Network and ports

```text
Browser --HTTP :80--> DronService --localhost :9997--> MediaMTX API
Browser --HLS :8888-------------------------------> MediaMTX
RTSP clients --RTSP :554--------------------------> MediaMTX
DronService --HTTP/RTSP--> saved or discovered cameras
```

MediaMTX API must remain bound to `127.0.0.1:9997`. Do not forward port 80,
8888, 554 or temporary camera-proxy ports from the public internet.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `MEDIAMTX_URL` | `http://127.0.0.1:9997` | MediaMTX Control API |
| `MEDIAMTX_USER` / `MEDIAMTX_PASSWORD` | empty | server-side Control API credentials |
| `MEDIAMTX_CONFIG_PATH` | `/usr/local/etc/mediamtx/mediamtx.yml` | active configuration file |
| `DRONSERVICE_ADDR` | `:80` | HTTP listen address |
| `DRONSERVICE_DATA_DIR` | `/var/lib/dronservice` | persistent camera/update state |
| `DRONSERVICE_UPDATE_REPOSITORY` | empty | public `owner/repository` for releases |
| `DRONSERVICE_CAMERA_PROXY_TTL` | `15m` | camera web proxy lifetime |
| `DRONSERVICE_CAMERA_PROXY_ADDR` | `:0` | temporary proxy listener |
| `DRONSERVICE_CAMERA_NETWORK_INTERFACES` | `eth0,wlan0` | root helper interface allowlist |
| `DRONSERVICE_DISCOVERY_INTERFACE` | automatic | explicit camera discovery interface override |
| `DRONSERVICE_STREAM_PREVIEW_TTL` | `10m` | HLS preview lifetime |
| `DRONSERVICE_HLS_PUBLIC_URL` | detected Pi IPv4 on port 8888 | browser HLS base URL |

Secrets belong in a root-managed environment file and must never be committed.

## Временный доступ к web-интерфейсу IP-камеры

На странице `/ip-cameras` кнопка доступа открывает камеру напрямую, если её IPv4-адрес входит в одну из локальных подсетей Raspberry Pi. Для камеры из другой подсети DronService запускает отдельный временный reverse proxy и возвращает браузеру адрес Raspberry Pi с временным портом.

По умолчанию proxy работает 15 минут и слушает случайный свободный порт на всех интерфейсах. Параметры можно изменить:

```text
DRONSERVICE_CAMERA_PROXY_TTL=15m
DRONSERVICE_CAMERA_PROXY_ADDR=:0
```

Межсетевой экран Raspberry Pi должен разрешать выбранный порт. Multicast discovery
подтверждает присутствие камеры на Ethernet, но сам по себе не создаёт IPv4
маршрут. Например, Raspberry Pi `192.168.88.254/24` может обнаружить Dahua
`192.168.1.108/24`, но не сможет открыть HTTP через gateway
`192.168.88.1`. В этом случае DronService просит отдельный root-owned helper,
который systemd запускает от `admin`,
временно добавить свободный secondary address наподобие
`192.168.1.254/24` на подтверждённый `eth0`.

Основной процесс остаётся пользователем `admin` без `CAP_NET_ADMIN`. Helper
принимает только типизированную JSON-заявку для камеры из store, повторно
проверяет IP/MAC/prefix/interface allowlist, выполняет ARP duplicate-address
detection и назначает адресу kernel `valid_lft`. Адрес автоматически исчезает
по TTL даже после аварии сервиса. Настройте разрешённые физические интерфейсы в
`/etc/dronservice/camera-network.conf` и перезапустите path unit после изменения.
Helper binary остаётся `root:root`, но только его процесс получает
`CAP_NET_ADMIN` и `CAP_NET_RAW`; `CAP_DAC_OVERRIDE` не используется. `arping`
определяется через безопасный executable lookup, поэтому compatibility symlink
не нужен. На Raspberry Pi OS требуется пакет `iputils-arping`.
Обычно interface сохраняется из входящего discovery packet автоматически. Для
диагностики или неоднозначной многосетевой конфигурации задайте override в
`/etc/dronservice/dronservice.env`, например
`DRONSERVICE_DISCOVERY_INTERFACE=eth0`; installer и OTA этот файл не заменяют.

## Предпросмотр видеопотока

Кнопки Main и Sub в списке IP-камер создают временный
`sourceOnDemand` path в MediaMTX для выбранного RTSP-потока. Кнопка в
списке аналоговых камер открывает сохранённый V4L2-режим
(YUYV/MJPEG, разрешение и FPS) через FFmpeg и перекодирует его в
H.264/HLS для браузера. Перед аналоговым предпросмотром установите
видео-runtime:

```bash
sudo ./deploy/install-video-runtime.sh
```

Браузер получает только HLS-адрес; RTSP-логин и пароль IP-камеры
остаются на сервере. При закрытии модального окна временный path
удаляется, а ограничение времени служит страховкой для незакрытой
вкладки:

```text
DRONSERVICE_STREAM_PREVIEW_TTL=10m
DRONSERVICE_HLS_PUBLIC_URL=http://192.168.1.147:8888
```

Если `DRONSERVICE_HLS_PUBLIC_URL` не указан, DronService использует основной
IPv4 Raspberry Pi и HLS-порт `8888`. Этот TCP-порт должен быть доступен
браузеру. IP-поток MediaMTX передаёт без перекодирования, поэтому кодек
выбранного Main/Sub должен поддерживаться браузером.

В таблице MediaMTX под каждым потоком показываются настройки его
источника: разрешение, FPS и битрейт. Для аналогового H.264-потока
битрейт обозначается как динамический (`CRF 23`); для несвязанных внешних
path неизвестные значения показываются как `—`.

DronService is a Go service for managing MediaMTX, video devices and IP cameras on Linux ARM64 devices such as Raspberry Pi 5.

## MediaMTX configuration

The MediaMTX page contains a manual editor for the active `mediamtx.yml`.
Manual saves validate YAML and require a top-level `paths` mapping. MediaMTX
hot-reloads valid file changes.

Persistent camera stream changes are written directly into the matching entry
under `paths`. DronService preserves the file header, comments, ordering and
all unrelated path blocks. Temporary preview paths continue to use the Control
API and are never persisted. The managed configuration path defaults to
`/usr/local/etc/mediamtx/mediamtx.yml` and can be overridden with
`MEDIAMTX_CONFIG_PATH`.

## HTTP access

DronService does not require a login and is intended for a trusted local
network. Do not expose its HTTP port directly to the internet. Browser requests
that change state are restricted to the same origin, and standard security
headers are applied to every response.

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

1. downloads the binary, deployment manifest, units/scripts, checksums and signatures from the requested GitHub tag;
2. verifies the signed checksum list, SHA-256 of every asset and the binary RSA signature;
3. verifies that the signed binary reports the requested version;
4. installs it under `/usr/local/lib/dronservice/releases/vX.Y.Z`;
5. atomically changes the `current` symlink;
6. restarts DronService and checks `/api/health` and `/api/version`;
7. applies deployment schema migrations and reloads systemd before activation;
8. restores the previous binary and deployment files if migration, validation or health checks fail.

The release manifest is `deployment-manifest.json`. Its `schemaVersion` is
validated before any deployment file is installed, preventing a newer updater
layout from being applied by code that does not understand it.

There is deliberately no `dronservice-update.timer`.

## Bootstrap another Raspberry Pi

Copy the release binary and the `deploy` directory to the target once, then run:

```bash
sudo apt-get update
sudo apt-get install -y iproute2 iputils-arping
sudo ./deploy/install-dronservice.sh ./dronservice-linux-arm64 owner/repository
```

The host must be ARM64 and contain the `admin` user. The installer creates all
sandbox writable paths before starting a unit, migrates the legacy MediaMTX
configuration, installs the public verification key and systemd units, enables
manual update/install path units, and verifies both `/api/health` and the exact
`/api/version`. Device configuration remains under `/var/lib/dronservice` and
is not included in release artifacts.

Install MediaMTX from its page in DronService, or place a strict semantic
version in `/var/lib/dronservice/install-mediamtx.request`; the root oneshot
unit downloads and checksum-verifies the ARM64 archive. Install FFmpeg/V4L2
runtime once with `sudo ./deploy/install-video-runtime.sh`.

## Rollback and troubleshooting

The updater rolls back the active release symlink and backed-up deployment
files automatically when migration, restart, health or version checks fail.
For an operator rollback, repoint `/usr/local/lib/dronservice/current` to a
previous directory under `/usr/local/lib/dronservice/releases/`, update the
`/usr/local/bin/dronservice` symlink and restart `dronservice.service`.

- `systemctl status dronservice mediamtx` shows service failures.
- `journalctl -u dronservice -u mediamtx -n 200` shows recent logs without stored credentials.
- `curl http://127.0.0.1/api/health` checks DronService locally.
- `curl http://127.0.0.1:9997/v3/paths/list` checks MediaMTX locally.
- A `no route to host` camera error requires an OS route/VLAN/interface fix;
  если discovery сохранил MAC, mask и interface, temporary subnet helper может
  безопасно создать ограниченный lease. При неизвестной mask адрес не угадывается.
- Проверить helper: `systemctl status dronservice-camera-network.path` и
  `journalctl -u dronservice-camera-network.service -n 100`.
- Проверить executable/capabilities: `command -v arping` и
  `systemctl show dronservice-camera-network.service -p User -p CapabilityBoundingSet`.
- Ручное восстановление: остановите proxy, дождитесь TTL либо удалите только
  адрес, указанный в `/var/lib/dronservice/camera-network.leases.json`, командой
  `sudo ip address del ADDRESS/PREFIX dev INTERFACE`. Не удаляйте постоянные
  адреса интерфейса.
- If MediaMTX config saving is denied after upgrading an old installation,
  rerun the current installers so `/usr/local/etc/mediamtx` is owned by `admin`.

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
remain `unknown` until DronService successfully reads an authenticated camera
configuration. That success is stored as positive initialization evidence and
is not erased by a later inconclusive discovery response. An explicit
uninitialized response still takes precedence.

For an initialized camera with saved server-side credentials, DronService can
read the main and sub-stream resolution/FPS from Dahua's `Encode`
configuration and can change the static IPv4 address, subnet mask and gateway.
Перед CGI-изменением DronService проверяет Digest authentication и определяет
реальное семейство полей прошивки (`Network.eth0` или `Network.eth0[0]`). Смена
IP может временно сделать камеру недоступной; не отключайте питание до проверки
нового адреса и используйте старый IP/MAC из camera store для восстановления.
The camera web interface is opened directly only when the camera belongs to a
local Raspberry Pi subnet; otherwise DronService issues a temporary reverse
proxy URL as described above.
