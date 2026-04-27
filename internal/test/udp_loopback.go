// Package test: UDP loopback probe for environments that don't route
// 127.0.0.1 UDP cleanly.
//
// The staticsources/rtp and staticsources/mpegts UDP-source tests
// (TestSourceUDP and friends) rely on a sender to deliver packets to
// a recorder listener on 127.0.0.1. On native Linux this is reliable;
// on Docker-on-macOS the Linux VM's loopback handling differs subtly
// and packets sent to 127.0.0.1 (and the multicast 238.0.0.1) don't
// always reach a listener bound on the same address. The tests then
// time out at the default Go test timeout — 600s under -timeout=600s,
// which is what `make test` configures — surfacing as a known-fail
// red FAIL at the end of an otherwise green run.
//
// Rather than annotating each affected environment in the test
// runner's config, this probe runs at the start of each affected
// test, exercises the same round-trip pattern the test will use, and
// calls t.Skip when the environment can't support it. The probe is
// fast (≤200ms in the failure case, instant in the success case) and
// adds essentially zero overhead on environments where the test
// proceeds normally.
//
// See recorder/docs/testing-notes.md §1 and §5 for the full
// background.
package test

import (
	"net"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// udpLoopbackProbeDeadline caps how long the probe waits for the
// listener to receive its own packet. 200ms is well above any
// legitimate native-Linux loopback round-trip and well below any
// useful test-timeout fallback; a probe that hits this deadline is
// almost certainly an environment where the loopback isn't usable at
// all.
const udpLoopbackProbeDeadline = 200 * time.Millisecond

// brokenLoopbackEnvironment returns a non-empty reason when the
// current process is running in an environment whose UDP loopback /
// multicast routing is known to misbehave for the staticsources
// TestSourceUDP test patterns:
//
//   - Native macOS (runtime.GOOS == "darwin"): per
//     recorder/docs/testing-notes.md §1, multicast-via-interface
//     subtests hang because macOS picks lo0 as the multicast-
//     capable interface and lo0 multicast routes don't deliver as
//     the test expects.
//
//   - Docker Desktop on macOS / Windows: the container reports
//     GOOS=linux but the underlying VM is linuxkit (Docker Desktop)
//     or has Microsoft kernel signatures; loopback packets routed
//     through the VM's bridged networking don't reach a 0.0.0.0
//     listener. Detected by reading /proc/version.
//
// Returns "" on native Linux (CI runners, real recorder hosts), where
// the tests run normally.
func brokenLoopbackEnvironment() string {
	if runtime.GOOS == "darwin" {
		return "macOS host: multicast-via-interface routes through lo0 " +
			"and packets do not deliver to a 0.0.0.0 listener as the test expects"
	}
	if runtime.GOOS == "linux" {
		if data, err := os.ReadFile("/proc/version"); err == nil {
			procVersion := strings.ToLower(string(data))
			switch {
			case strings.Contains(procVersion, "linuxkit"):
				return "Docker Desktop on macOS (linuxkit kernel): UDP loopback " +
					"routes through the Linux VM's bridge and does not deliver " +
					"to a 0.0.0.0 listener as the test expects"
			case strings.Contains(procVersion, "microsoft"):
				return "WSL2 / Docker Desktop on Windows (Microsoft kernel): " +
					"UDP loopback routes through the Linux VM's bridge and does " +
					"not deliver to a 0.0.0.0 listener as the test expects"
			}
		}
	}
	return ""
}

// RequireUDPLoopback skips the test on environments whose UDP
// loopback / multicast routing doesn't support the staticsources
// TestSourceUDP test patterns. The detection is environment-based
// (runtime.GOOS, /proc/version) rather than runtime-probe-based
// because the failing pattern is one specific subtest's
// multicast-via-interface flow — a unicast probe passes on macOS
// even though the affected subtest still hangs at 600s.
//
// On native Linux (CI runners, real recorder hosts) this is a
// no-op and the test proceeds normally. On environments listed in
// brokenLoopbackEnvironment, the test is skipped with a pointer to
// testing-notes.md §5.
//
// As a defense-in-depth, the probe also performs an actual UDP
// round-trip: 0.0.0.0 listener, 127.0.0.1 sender, 200ms deadline.
// This catches future Linux-but-broken environments without needing
// a brokenLoopbackEnvironment update; if the round-trip fails, skip
// regardless of GOOS.
func RequireUDPLoopback(t *testing.T) {
	t.Helper()

	if reason := brokenLoopbackEnvironment(); reason != "" {
		t.Skipf("UDP loopback / multicast-via-interface tests are documented "+
			"as flaky in this environment: %s "+
			"(see recorder/docs/testing-notes.md §5)", reason)
		return
	}

	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Skipf("UDP loopback probe: cannot bind wildcard listener: %v "+
			"(see recorder/docs/testing-notes.md §5)", err)
		return
	}
	defer listener.Close()

	listenerAddr := listener.LocalAddr().(*net.UDPAddr)
	target := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: listenerAddr.Port}

	client, err := net.DialUDP("udp4", nil, target)
	if err != nil {
		t.Skipf("UDP loopback probe: cannot dial 127.0.0.1:%d: %v "+
			"(see recorder/docs/testing-notes.md §5)", target.Port, err)
		return
	}
	defer client.Close()

	msg := []byte("raikada-udp-loopback-probe")
	if _, err := client.Write(msg); err != nil {
		t.Skipf("UDP loopback probe: write failed: %v "+
			"(see recorder/docs/testing-notes.md §5)", err)
		return
	}

	if err := listener.SetReadDeadline(time.Now().Add(udpLoopbackProbeDeadline)); err != nil {
		t.Skipf("UDP loopback probe: cannot set deadline: %v "+
			"(see recorder/docs/testing-notes.md §5)", err)
		return
	}
	buf := make([]byte, len(msg)+16)
	n, _, err := listener.ReadFromUDP(buf)
	if err != nil {
		t.Skipf("UDP loopback unavailable in this environment "+
			"(packet sent to 127.0.0.1:%d did not reach a 0.0.0.0 listener "+
			"within %s): %v "+
			"(see recorder/docs/testing-notes.md §5)",
			target.Port, udpLoopbackProbeDeadline, err)
		return
	}
	if string(buf[:n]) != string(msg) {
		t.Skipf("UDP loopback delivered unexpected bytes; the environment may "+
			"be filtering or rewriting loopback traffic "+
			"(see recorder/docs/testing-notes.md §5)")
		return
	}
}
