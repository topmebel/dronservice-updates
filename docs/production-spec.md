# DronService Production Specification

## Metadata

**Author:** DronService project owner / Codex

**Date:** 2026-08-18

**Status:** Approved (implementation explicitly requested)

**Reviewers:** project owner

## Context

DronService is the unprivileged control plane for a Raspberry Pi 5 video gateway. It owns the browser API and translates MediaMTX, FFmpeg, V4L2 and camera-specific behavior into stable application concepts. The service is intended for a trusted LAN and must not be exposed directly to the internet.

The existing repository already supplies the health/version/stream vertical slice, responsive server-rendered UI, Dahua and UNV discovery, V4L2 scanning, MediaMTX integration, previews and signed binary updates. This specification governs closing the remaining production gaps without replacing working implementations.

## Functional Requirements

- FR-1: The service MUST expose health, version, stream, camera, MediaMTX, system/network and update APIs through DronService-owned models.
- FR-2: Camera state MUST distinguish configured, idle, starting, online, offline and error; MediaMTX `online=false` MUST NOT alone imply failure.
- FR-3: Dahua DHIP, ONVIF/UNV and V4L2 cameras MUST be discoverable and persisted under `/var/lib/dronservice` without persisting credentials in public responses or logs.
- FR-4: Persistent camera changes MUST update only their MediaMTX `paths` entry while preserving comments, file preamble and unrelated entries.
- FR-5: Manual MediaMTX saves MUST validate YAML, require a top-level `paths` mapping, create a backup and use an atomic replacement.
- FR-6: Camera web proxy sessions MUST expire after a configurable TTL, accept only saved/discovered camera targets, reject unsafe targets, preflight TCP reachability and return route/connectivity diagnostics without credentials.
- FR-7: Installation MUST provision directories before starting sandboxed units, install MediaMTX before DronService, verify both services and roll back changed application/deployment state on failure.
- FR-8: Releases MUST contain the ARM64 binary, checksum, RSA signature, public key, deployment assets and a versioned deployment manifest.
- FR-9: Updates MUST verify HTTPS downloads, checksum, RSA signature and binary version; atomically switch releases; apply versioned deployment migrations; health-check; and roll back binary and deployment state on failure.
- FR-10: Update discovery MUST be automatic while installation MUST require an operator action, and progress MUST be exposed as JSON and refreshed by the UI.

## Non-Functional Requirements

- **NFR-1:** The Linux ARM64 application MUST run as `admin`, not root, and bind privileged HTTP/RTSP ports only through scoped capabilities.
- **NFR-2:** HTTP clients and servers MUST have finite timeouts; external operations MUST accept cancellation.
- **NFR-3:** State-changing browser requests MUST be same-origin and all responses MUST receive security headers.
- **NFR-4:** The project MUST pass `gofmt`, `go vet`, `go test`, applicable race tests and a static Linux ARM64 build.
- **NFR-5:** systemd units MUST use sandboxing and MUST NOT reference a writable path that installation has not created.
- **NFR-6:** Secrets and credential-bearing RTSP URLs MUST NOT be committed, returned to browsers unnecessarily or logged.

## Acceptance Criteria

### AC-1: API baseline (FR-1, FR-2, FR-3)

Given a local build, when the full test suite runs, then health/version/stream and camera API contract tests pass.

### AC-2: MediaMTX preservation (FR-4, FR-5)

Given a commented MediaMTX file with unrelated paths, when one camera is added, changed or removed, then only that path changes, a backup exists, and the resulting YAML is valid with top-level `paths`.

### AC-3: Proxy diagnostics (FR-6)

Given an unapproved, loopback, multicast or malformed target, when proxy creation is requested, then it is rejected before listening; given a routing/connect failure, when proxy creation is requested, then the response contains Pi IP, camera IP, interface/route result and the original connect class.

### AC-4: Clean installation (FR-7)

Given a clean ARM64 host, when the installer runs as root, then all `ReadWritePaths` exist before either service starts and both local health probes pass; given a failed probe, when installation aborts, then it restores the prior installation.

### AC-5: Transactional updates (FR-8, FR-9)

Given a signed release with a deployment manifest, when update is confirmed, then all assets are verified and installed transactionally; given any verification, migration or health failure, when rollback runs, then it restores the prior binary, units and active configuration.

### AC-6: Operator-controlled update (FR-10)

Given a newer semantic version, when periodic UI polling runs, then it displays availability/progress but does not install until explicit confirmation.

### AC-7: Production invariants (FR-1, FR-7, FR-8, FR-9, FR-10)

Given the production units and HTTP middleware, when invariant/security tests run, then the process user, capabilities, sandbox, same-origin enforcement, headers and secret redaction satisfy NFR-1 through NFR-6.

## Edge Cases

- EC-1: MediaMTX is unavailable, returns non-JSON, times out or changes optional response fields.
- EC-2: A camera is idle because `runOnDemand` has no reader.
- EC-3: A camera address changes, has no route, refuses TCP, times out or redirects to an absolute private URL.
- EC-4: The active YAML is absent, malformed, lacks `paths`, is read-only or changes concurrently.
- EC-5: Update download is truncated, checksum/signature/version mismatches, disk space is insufficient or systemd restart fails.
- EC-6: A deployment migration is interrupted after files are staged but before activation.
- EC-7: A clean host lacks FFmpeg, curl, OpenSSL, MediaMTX directories or the `admin` user.

## API Contracts

`GET /api/health`, `GET /api/version`, `GET /api/streams`, `GET /api/application/update`, `POST /api/application/update`, and the existing camera/MediaMTX endpoints retain their current contracts.

```ts
type Health = { status: "ok" };
type Version = { version: string; commit: string; builtAt: string };
type StreamState = "configured" | "idle" | "starting" | "online" | "offline" | "error";
type Stream = { name: string; state: StreamState; ready: boolean; readers: number; inboundBytes: number; outboundBytes: number };
type UpdateStatus = { state: string; version: string; message: string; updatedAt: string };
type ProxyDiagnostic = { piIPs: string[]; cameraIP: string; interface?: string; route: string; connectError?: string };
type APIError = { error: string; diagnostic?: ProxyDiagnostic };
```

Existing endpoint request/response contracts remain backward compatible. New diagnostic and deployment fields are additive.

## Data Models

| Entity | Required fields | Constraints |
|---|---|---|
| Camera | stable ID, kind, name, address, streams, state | validated ID/IP; secrets stored server-side only |
| Stream | name, source kind, state, readiness, traffic | public model independent of MediaMTX schema |
| MediaMTX config | raw YAML, path entries | valid YAML; top-level mapping containing `paths` |
| Update status | state, version, message, timestamp | atomic JSON file; no secret/error command lines |
| Deployment manifest | schema version, application version, asset hashes, migrations | signed indirectly by release signature/checksum set |

## Out of Scope

- OS-1: Internet-facing authentication and multi-user authorization are excluded; this deployment remains trusted-LAN only.
- OS-2: Recording, archive/timeline, PTZ and a database are excluded because they are not required for this production baseline.
- OS-3: Node.js and frontend frameworks are excluded; the embedded HTML/CSS/JS UI remains the supported interface.
- OS-4: Hardware success is not emulated in WSL; device-dependent checks require the target Raspberry Pi.
