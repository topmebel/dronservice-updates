# AGENTS.md — DronService

## Project overview

DronService is a Go backend for managing a video system running on Raspberry Pi 5 and, later, Orange Pi 5.

The application is intended to become the central control service for:

- MediaMTX
- IP cameras
- USB video capture devices / V4L2
- analog cameras connected through USB capture devices
- FFmpeg processes
- RTSP streams
- camera state and health
- Raspberry Pi / Linux system information
- later: recording, ONVIF, WebSocket updates, frontend, monitoring and other video-gateway/NVR features

DronService is the central application. MediaMTX is an internal video transport/service component and must not define the public DronService API.

## Development environment

Development machine:

- Windows 11
- WSL2 Ubuntu
- Go 1.26
- project directory: `~/projects/DronService`

Target device:

- Raspberry Pi 5
- Linux ARM64 / `aarch64`
- development IP currently: `192.168.1.147`
- SSH user currently: `admin`

Do not store SSH passwords, MediaMTX passwords, tokens, or other secrets in this repository.

The project should remain portable to Orange Pi 5 and other Linux ARM64 systems whenever practical.

## Technology stack

Current stack:

- Go
- Go standard library, especially `net/http`
- MediaMTX Control API
- FFmpeg
- V4L2
- Linux
- systemd

Prefer the Go standard library unless an external dependency provides a clear practical benefit.

Do not introduce Gin, Fiber, GORM, large frameworks, dependency-injection frameworks, or similar abstractions without a concrete need.

## Current architecture

The intended dependency direction is:

```text
Browser / future frontend
        |
        v
DronService HTTP API
        |
        v
Application / service layer
        |
        +----------+----------+----------+
        |          |          |          |
        v          v          v          v
    MediaMTX    FFmpeg      V4L2       Linux
        |
        v
 cameras / video streams
```

The frontend must communicate with DronService, not directly with the MediaMTX Control API.

MediaMTX-specific concepts must be translated into DronService domain/application concepts before they are exposed through the public API.

## Current project structure

The project currently follows approximately this structure:

```text
DronService/
├── go.mod
├── main.go
└── internal/
    ├── mediamtx/
    │   ├── client.go
    │   └── models.go
    └── stream/
        └── service.go
```

Do not create a large directory hierarchy preemptively. Add packages when responsibilities actually become distinct.

The Go module name is currently:

```text
DronService
```

## Current HTTP API

### GET /api/health

Expected response:

```json
{
  "status": "ok"
}
```

### GET /api/streams

This endpoint obtains information from MediaMTX and converts it into DronService's own stream representation.

Do not expose the raw MediaMTX response as the long-term public API.

A DronService response may look conceptually like:

```json
{
  "streams": [
    {
      "name": "camera1",
      "online": false,
      "ready": false,
      "available": false,
      "readers": 0,
      "inboundBytes": 0,
      "outboundBytes": 0
    }
  ]
}
```

This model will evolve as DronService gains its own camera-state model.

## MediaMTX

MediaMTX Control API is enabled.

### Displayed RTSP addresses

RTSP URLs shown or copied by the DronService interface must always contain a literal Raspberry Pi IPv4 address, never a hostname or mDNS name such as `raspberry.local`.

Use this preference order for the primary displayed RTSP URL:

1. LAN IPv4 address;
2. Wi-Fi IPv4 address;
3. `127.0.0.1` only when no usable interface address exists.

An additional ZeroTier RTSP URL may be displayed when a ZeroTier IPv4 address is available. Do not change displayed RTSP URLs back to request hostnames in future UI work.

Current endpoint used by DronService:

```text
GET /v3/paths/list
```

A real response from the installed MediaMTX version contains fields such as:

```json
{
  "itemCount": 2,
  "pageCount": 1,
  "items": [
    {
      "name": "camera1",
      "confName": "camera1",
      "ready": false,
      "readyTime": null,
      "available": false,
      "availableTime": null,
      "online": false,
      "onlineTime": null,
      "source": null,
      "tracks": [],
      "tracks2": [],
      "readers": [],
      "inboundBytes": 0,
      "outboundBytes": 0,
      "inboundFramesInError": 0,
      "bytesReceived": 0,
      "bytesSent": 0
    }
  ]
}
```

Keep MediaMTX response structures inside `internal/mediamtx`.

Do not make public DronService API types depend directly on the MediaMTX API schema.

## Current cameras

MediaMTX currently has two configured paths:

```text
camera1 -> /dev/video0
camera2 -> /dev/video2
```

Both are analog/video capture sources handled by FFmpeg.

MediaMTX uses `runOnDemand` to start FFmpeg.

Therefore:

```text
online == false
```

does **not** necessarily mean a camera is broken or disconnected.

It can simply mean that no client is currently requesting the stream and FFmpeg has not been started.

DronService must eventually provide a higher-level camera state instead of exposing MediaMTX state as-is.

Target state model should consider states such as:

```text
configured
idle
starting
online
error
offline
```

The exact semantics should be designed from observable system state rather than guessed.

Potential inputs include:

- MediaMTX path state
- V4L2 device presence
- FFmpeg process state
- MediaMTX source state
- recent process errors
- stream readiness
- explicit user actions

## MediaMTX configuration

Configuration is provided through environment variables:

```text
MEDIAMTX_URL
MEDIAMTX_USER
MEDIAMTX_PASSWORD
```

During development from WSL, MediaMTX can be accessed over the LAN, currently at:

```text
http://192.168.1.147:9997
```

Example development invocation:

```bash
MEDIAMTX_URL=http://192.168.1.147:9997 \
MEDIAMTX_USER=admin \
MEDIAMTX_PASSWORD=<secret> \
go run .
```

Never commit the actual password.

In production DronService will run directly on the Raspberry Pi and should normally use:

```text
MEDIAMTX_URL=http://127.0.0.1:9997
```

The preferred final security model is:

```text
LAN / browser
      |
      v
DronService
      |
      | localhost only
      v
MediaMTX Control API :9997
```

The MediaMTX Control API should not need to be exposed to the LAN in production.

## Go coding rules

Write idiomatic, straightforward Go.

### Error handling

Return errors for normal failure conditions.

Do not use `panic` for expected runtime failures.

Wrap errors with useful context:

```go
return nil, fmt.Errorf("request MediaMTX: %w", err)
```

Errors should tell the operator what operation failed.

### Context

Use `context.Context` for operations involving:

- HTTP requests
- external processes
- FFmpeg
- potentially blocking Linux/device operations
- operations that should stop during shutdown

Propagate request contexts when appropriate.

### HTTP

Always use sensible HTTP client and server timeouts.

Do not use an unbounded production HTTP client.

Use appropriate HTTP status codes and JSON responses.

Avoid leaking internal errors, credentials, command lines containing secrets, or unnecessary implementation details to remote clients.

### Processes

When DronService begins managing FFmpeg:

- use `exec.CommandContext` where appropriate
- capture and preserve useful stderr output
- track process lifecycle explicitly
- avoid orphaned FFmpeg processes
- support clean termination
- distinguish user-requested shutdown from process failure
- do not construct unsafe shell command strings from user input

Prefer argument arrays through `exec.Command` / `exec.CommandContext` instead of invoking a shell.

### Concurrency

Use goroutines and channels when they simplify a real concurrency problem.

Do not introduce concurrency merely because Go supports it.

Protect shared mutable state appropriately.

Avoid goroutine leaks.

### Interfaces

Do not create interfaces preemptively.

Introduce an interface when there is a real boundary requiring multiple implementations, meaningful test substitution, or decoupling.

Prefer concrete types until an abstraction is justified.

### Configuration

Do not hardcode:

- passwords
- API credentials
- machine-specific secrets
- deployment-specific addresses when they should be configurable

Validate important configuration at startup.

### Logging

Use structured or consistently formatted logging.

Never log passwords or authentication headers.

Logs should be useful when DronService runs unattended under systemd.

## Linux and ARM64 requirements

Production target is Linux ARM64.

Avoid accidental dependencies on Windows or WSL behavior.

Platform-specific functionality should be isolated behind packages with clear responsibilities.

DronService should eventually be buildable from WSL with:

```bash
GOOS=linux GOARCH=arm64 go build
```

Do not assume Raspberry-Pi-specific GPIO libraries unless a feature actually requires them.

Where possible, use generic Linux interfaces such as:

- V4L2
- `/dev`
- procfs/sysfs
- standard networking
- systemd integration

This helps retain Orange Pi compatibility.

## API design rules

DronService owns its public API.

Do not expose third-party API schemas directly simply because doing so is convenient.

For example:

```text
MediaMTX Path -> DronService Stream
```

and eventually:

```text
V4L2 device
MediaMTX path
FFmpeg process
       |
       v
DronService Camera
```

The frontend should not need to know whether a camera is implemented with MediaMTX, FFmpeg, V4L2, ONVIF, or another internal component.

Avoid breaking API changes when an internal dependency changes.

## Security rules

Assume DronService may eventually be reachable from a LAN or remotely through another secured layer.

Therefore:

- never commit credentials
- never expose MediaMTX credentials to the frontend
- do not accept arbitrary shell commands
- validate camera names, paths, URLs, device identifiers and configuration input
- treat RTSP URLs as potentially containing credentials
- avoid logging full credential-bearing RTSP URLs
- restrict privileged Linux operations
- prefer least-privilege service users
- keep MediaMTX Control API localhost-only in production when possible

## Testing and quality checks

Before considering a Go change complete, run as applicable:

```bash
gofmt -w .
go vet ./...
go test ./...
go build ./...
```

New business/application logic should have tests when those tests provide practical value.

MediaMTX parsing and state mapping are good candidates for table-driven tests.

Avoid tests that merely duplicate implementation details.

## Deployment direction

Development happens in WSL.

Production binary runs on Raspberry Pi.

The intended deployment flow is eventually:

```text
WSL
 |
 +-- test
 +-- build linux/arm64
 +-- copy binary to Raspberry Pi
 +-- restart DronService systemd unit
 +-- health check
```

This can later become a `Makefile`, deployment script, or CI workflow.

DronService should ultimately run as a systemd service.

Do not require a Go toolchain on the Raspberry Pi for production deployment.

## Planned capabilities

Likely future areas include:

- V4L2 device discovery
- USB capture capability detection
- FFmpeg lifecycle management
- IP camera configuration
- ONVIF discovery
- ONVIF PTZ
- stream health
- WebSocket/SSE live status
- recording
- snapshots
- archive/timeline
- system CPU/RAM/temperature/storage monitoring
- hardware video encoding where supported
- user authentication and authorization
- frontend
- configuration persistence
- OTA/update mechanism
- Orange Pi 5 support

These are directions, not requirements to implement immediately.

Do not add infrastructure for a future feature until the current task needs it.

## Current priorities

Near-term work should focus on:

1. Keep the MediaMTX client reliable and isolated.
2. Establish DronService-owned stream/camera models.
3. Define meaningful camera states.
4. Detect V4L2 devices and correlate them with configured cameras.
5. Add FFmpeg/process observability where needed.
6. Improve HTTP API structure as endpoints grow.
7. Add graceful shutdown and production-grade server configuration.
8. Add focused tests.
9. Prepare ARM64 deployment and systemd service.
10. Only then expand into frontend and persistence as required.

## Working style for coding agents

Before making substantial changes:

1. Inspect the existing repository.
2. Understand the code that already works.
3. Identify the smallest useful change.
4. State which files will be modified when the change is non-trivial.
5. Preserve working behavior unless the task explicitly changes it.

Do not rewrite the entire project to match a preferred architecture.

Do not perform broad refactors while implementing a small feature unless the existing structure genuinely prevents the feature.

If an architectural problem is discovered, explain it and propose a focused correction.

Prefer incremental commits and changes that remain buildable.

When uncertain about the installed MediaMTX version or API schema, inspect the actual current response/configuration instead of assuming another version's behavior.

When implementing Raspberry Pi / Linux functionality, distinguish between:

- code that can be tested in WSL
- code requiring the Raspberry Pi
- code requiring actual `/dev/video*` hardware

Do not fake hardware success.

## Guidance for explanations

The project owner has previous Laravel experience and is learning Go.

When explaining non-obvious Go code, concise comparisons with Laravel/PHP concepts can be useful, for example:

```text
Laravel Controller -> Go HTTP handler
Laravel Service    -> Go service struct
DTO / Model        -> Go struct
HTTP client        -> MediaMTX Client
response()->json() -> json.Encoder
```

However, write idiomatic Go rather than attempting to reproduce Laravel architecture in Go.

The primary goal is a reliable DronService, with learning Go happening through the real project.
