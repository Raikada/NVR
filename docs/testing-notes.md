# Testing Notes

Practical guidance for running the recorder's test suite locally. The
authoritative test surface is the Docker-based `make test` target; this
document captures the environmental gotchas you will hit when running
the local-equivalent `go test ./internal/...` outside the container.
None of the issues below are bugs in the code under test — they are
host-environment artifacts that the Docker target works around.

## Quick reference

| Goal                                          | Command                                                     |
|-----------------------------------------------|-------------------------------------------------------------|
| Full coverage, reliable across machines       | `make test` (requires Docker)                               |
| Local quick check (subset, may exclude tests) | `go test -p 1 -count=1 ./internal/<pkg>` for the package(s) you touched |
| Local broad check (excluding flaky-on-host)   | See "Local broad-check command" below                        |

For the divergence-driven code work in
[`canonical-divergences.md`](canonical-divergences.md), the per-commit
gate is the single touched package run with `go test -count=1 -p 1
./internal/<pkg>`. Do not rely on a broad local run for green-light
decisions; use Docker.

## Environmental issues observed on macOS

Each is durable: every clean checkout exhibits the same pattern. None
have been investigated as bugs because the Docker target works.

### 1. UDP-loopback static-source tests hang

**Affected:** `internal/staticsources/rtp/TestSourceUDP`,
`internal/staticsources/mpegts/TestSource` (and similar).

**Symptom:** Test sends UDP packets to `127.0.0.1:9004` (and the
multicast `238.0.0.1:9004`) and waits on a channel for received units.
The channel never fires. With the default `go test` timeout the test
hangs indefinitely; with an explicit `-timeout=600s` it fails on the
deadline.

**Cause:** Some macOS configurations route loopback / multicast UDP
traffic in ways the test assumes won't happen — packets sent to
`127.0.0.1` aren't reaching the recorder's UDP listener bound to
`0.0.0.0`. The same tests pass under Linux in Docker.

**Workaround:** Exclude these packages from local test runs. The code
under test is media-pipeline territory and is not modified by any of
the open divergence work.

### 2. `internal/protocols/udp` RCVBUF unimplemented

**Affected:** `internal/protocols/udp/TestListen`.

**Symptom:** `read buffer size is unimplemented on the current
operating system` from the listener constructor.

**Cause:** macOS does not expose the same `SO_RCVBUF` semantics the
test asserts on. Linux does.

**Workaround:** Exclude `internal/protocols/udp` from local runs.

### 3. `internal/protocols/webrtc` UDP candidate gathering

**Affected:** `TestPeerConnectionCandidates/udp_random` and
`/udp` subtests.

**Symptom:** Test expects two ICE candidates from a UDP-mux config and
gets zero.

**Cause:** Likely related to (1) and (2) — the WebRTC stack cannot
gather UDP host candidates because the underlying UDP socket setup
fails or no usable interface presents itself.

**Workaround:** Exclude `internal/protocols/webrtc` from local runs.

### 4. Integration-test port collisions on default ports

**Affected:** `internal/core/*`, `internal/confwatcher/*`,
intermittently `internal/api/api_webrtc_test.go`.

**Symptom:** Subtests within `TestAPIProtocolGetNotFound`,
`TestAPIProtocolKick`, etc. fail at `require.Equal(t, true, ok)` from
`newInstance(...)` with elapsed time `0.00s`. The recorder under test
fails to construct, returning `(nil, false)`.

**Cause:** Each subtest starts a full recorder binding the standard
default ports (`:8554` RTSP, `:8000`/`:8001` RTP/RTCP, `:1935` RTMP,
`:8888` HLS, `:8889` WebRTC HTTP, `:8189` WebRTC ICE, `:8890` SRT,
`:9997` API). Sequential subtests run faster than macOS releases
TCP/UDP sockets from `TIME_WAIT` and from kernel UDP buffer release;
the next subtest's bind fails with `address already in use` and the
recorder construction returns false. `go test`'s default
package-parallelism (`-p $GOMAXPROCS`) makes this worse — multiple
test binaries hit the same default ports simultaneously.

**Workaround:** Run with `-p 1` (one package at a time) to suppress
inter-package collisions, and accept that intra-package subtest
collisions can still occur. Docker isolation is the only fully
reliable answer.

### 5. Stale `mediamtx` process recognition pattern

**Symptom:** Every test in `internal/api`, `internal/core`,
`internal/metrics`, `internal/playback` failing simultaneously with
errors like `expected: v1.2.3, actual: v0.0.0` — the test's HTTP
client is connecting to a previously-started mediamtx that's still
listening on `:9997`, not the test's own ephemeral server.

**Diagnosis:** `lsof -iTCP -sTCP:LISTEN | grep mediamtx`.

**Workaround:** Kill the stale process. `kill <pid>` then re-run.

This is not a code issue; it's a developer-environment artifact (a
prior `./mediamtx` invocation that wasn't shut down). Including it
here so a future agent recognizes the pattern instead of doing the
same archaeology.

## Local broad-check command

For when you want a local approximation of the test suite without
Docker:

```sh
go test -count=1 -p 1 -timeout=600s \
  $(go list ./internal/... \
    | grep -v '/internal/staticsources/rtp$' \
    | grep -v '/internal/staticsources/mpegts$' \
    | grep -v '/internal/protocols/udp$' \
    | grep -v '/internal/protocols/webrtc$')
```

This excludes the four packages that fail purely for environmental
reasons on macOS. `internal/core` and `internal/confwatcher` may still
exhibit the port-collision issues from §4 — re-run individually if
they fail; if they pass in isolation, the collision was the cause.

For full reliable coverage: `make test`.

## When to update this file

- A new environmental issue is found and reproduced on a clean
  checkout. Add a numbered entry following the pattern above.
- An environmental issue is fixed (typically: macOS networking
  improves, or a test is rewritten to be host-portable). Remove the
  entry and note the resolving commit in the PR.

## What this file is not

- Not a substitute for `make test` in CI or for a release-readiness
  check.
- Not an excuse to skip tests when modifying code. The per-commit
  gate stays the four-touched-packages run; the broader local
  approximation here is a sanity check, not a green-light.
