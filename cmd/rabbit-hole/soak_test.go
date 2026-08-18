// Package main — DF-027 soak regression test.
//
// The serve daemon wedged under its documented Quick Start (serve
// --demo-stream + remote gRPC + WS subscriber + attach cycles): 19.7GB
// RSS / 681% CPU in ~15min, /health timing out, SIGQUIT unable to complete
// its goroutine dump, SIGKILL required. Root cause (fixed in
// internal/attach): Pipeline.Run busy-looped with no pause and opened a
// new stream + classify + persist goroutine set per running session per
// poll iteration, leaking three parked goroutines per iteration.
//
// This test runs the FULL daemon as a real subprocess (real binary, real
// signals, real /proc RSS) under the documented combo and asserts:
//  1. RSS stays under a sane ceiling (500MB) for the whole soak,
//  2. /health responds in < 1s throughout,
//  3. SIGQUIT terminates the process promptly (no SIGKILL needed).
//
// Bounded: ~60s by default (override with RH_SOAK_DURATION, e.g. 5m), and
// skipped entirely under `go test -short` so the default suite stays fast.
package main

import (
	"bytes"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"gitlab.readydedis.com/rabbit-hole/rabbit-hole/internal/classify"
)

// safeBuf is a goroutine-safe writer buffer for capturing the daemon's
// stdout+stderr while the parent test watches for the demo-stream session
// line.
type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// filteredEnv returns the current environment with every variable whose
// name starts with any of the prefixes removed, plus the overrides.
// Ambient RABBITHOLE_* vars are known session contamination and MUST NOT
// leak into the daemon subprocess.
func filteredEnv(overrides []string, stripPrefixes ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		strip := false
		for _, p := range stripPrefixes {
			if strings.HasPrefix(kv, p) {
				strip = true
				break
			}
		}
		if !strip {
			env = append(env, kv)
		}
	}
	return append(env, overrides...)
}

// vmRSSKB reads the daemon's resident set size from /proc/<pid>/status.
func vmRSSKB(t *testing.T, pid int) int64 {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return -1 // process gone
	}
	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmRSS:"); ok {
			fields := strings.Fields(rest)
			if len(fields) >= 1 {
				kb, err := strconv.ParseInt(fields[0], 10, 64)
				if err == nil {
					return kb
				}
			}
		}
	}
	return -1
}

// healthLatency performs GET /health and returns the response time.
func healthLatency(addr string, timeout time.Duration) (time.Duration, error) {
	client := &http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Get("http://" + addr + "/health")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("health status %d", resp.StatusCode)
	}
	return time.Since(start), nil
}

// waitForHealth polls /health until the daemon answers.
func waitForHealth(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := healthLatency(addr, time.Second); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("daemon never answered /health within %s", timeout)
}

var demoSessionRe = regexp.MustCompile(`dogfood live stream enabled.*session=([0-9a-fA-F-]{36})`)

// waitForDemoSession scans the daemon log for the demo-stream session ID,
// which is the session the demo loop publishes flows into.
func waitForDemoSession(t *testing.T, out *safeBuf, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if m := demoSessionRe.FindStringSubmatch(out.String()); m != nil {
			return m[1]
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("daemon log never announced the demo-stream session:\n%s", out.String())
	return ""
}

// TestServeSoak_HelperProcess is the subprocess entry point: when
// RH_SOAK_HELPER=1 the test binary becomes the Rabbit-Hole daemon running
// the documented Quick Start combo and blocks until a signal kills it
// (SIGQUIT dumps + exits; SIGTERM/SIGINT shut down gracefully).
func TestServeSoak_HelperProcess(t *testing.T) {
	if os.Getenv("RH_SOAK_HELPER") != "1" {
		t.Skip("soak helper process")
	}

	serveCmd := newServeCmd()
	serveCmd.SetArgs([]string{
		"--no-ebpf",
		"--demo-stream",
		"--demo-every", "3",
		"--remote", os.Getenv("RH_SOAK_REMOTE"),
		"--addr", os.Getenv("RH_SOAK_ADDR"),
	})
	if err := serveCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "soak helper serve:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// TestServeSoak_DemoStreamRemoteWSAttach is the DF-027 acceptance soak:
// daemon + remote classify server + demo stream + WS subscriber
// connect/disconnect + attach/detach cycles + demo --spread seed on the
// same DB, with RSS and /health assertions throughout and a SIGQUIT
// termination check at the end.
func TestServeSoak_DemoStreamRemoteWSAttach(t *testing.T) {
	if testing.Short() {
		t.Skip("DF-027 soak test skipped in -short mode")
	}

	// --- Remote classify server (the serving side of --remote), in-process.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("classify-server listen: %v", err)
	}
	grpcSrv := classify.NewGRPCServer(
		classify.NewPatternServer(classify.WithServerVersion("soak")), "secret")
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)
	remote := lis.Addr().String() + "@secret"

	// --- Free port for the daemon + hermetic DB.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("port probe: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	soakDuration := 60 * time.Second
	if d := os.Getenv("RH_SOAK_DURATION"); d != "" {
		if parsed, err := time.ParseDuration(d); err == nil && parsed > 0 {
			soakDuration = parsed
		}
	}

	dbPath := t.TempDir() + "/rh-soak.db"

	// --- Spawn the daemon subprocess (real binary, real signals, real RSS).
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	out := &safeBuf{}
	cmd := exec.Command(exe, "-test.run=^TestServeSoak_HelperProcess$")
	cmd.Env = filteredEnv([]string{
		"RH_SOAK_HELPER=1",
		"RH_SOAK_ADDR=" + addr,
		"RH_SOAK_REMOTE=" + remote,
		"RABBITHOLE_DB_PATH=" + dbPath,
		"RABBITHOLE_LISTEN_ADDR=" + addr,
		"RABBITHOLE_RATE_LIMIT_ENABLED=false",
	}, "RABBITHOLE_", "RH_SOAK_")
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn daemon: %v", err)
	}
	pid := cmd.Process.Pid
	daemonExited := false
	t.Cleanup(func() {
		if !daemonExited {
			_ = cmd.Process.Kill() // safety net — never leave a soak daemon behind
		}
	})

	// Point parent-side CLI invocations (demo seed, attach/detach) at the daemon.
	t.Setenv("RABBITHOLE_DB_PATH", dbPath)
	t.Setenv("RABBITHOLE_LISTEN_ADDR", addr)

	waitForHealth(t, addr, 30*time.Second)
	demoSessionID := waitForDemoSession(t, out, 20*time.Second)

	// --- demo --spread seed on the SAME DB (documented Quick Start).
	if _, err := executeCLI(t, newDemoCmd(), "--spread", "--flows", "20"); err != nil {
		t.Fatalf("demo --spread seed failed: %v", err)
	}

	// --- Soak: sample RSS + /health while driving attach/detach cycles and
	// WS connect/disconnect against the live daemon.
	const rssCeilingKB = int64(500 * 1024) // 500MB — a healthy daemon sits ~40MB
	const healthBudget = time.Second

	startRSS := vmRSSKB(t, pid)
	maxRSS := startRSS
	var maxHealth time.Duration

	deadline := time.Now().Add(soakDuration)
	cycle := 0
	wsConnected := false
	for time.Now().Before(deadline) {
		// Health must stay responsive under load.
		lat, err := healthLatency(addr, 2*time.Second)
		if err != nil {
			t.Fatalf("health failed during soak: %v (latency so far: %v)", err, lat)
		}
		if lat > healthBudget {
			t.Fatalf("/health took %v (> %s) during soak", lat, healthBudget)
		}
		if lat > maxHealth {
			maxHealth = lat
		}

		// RSS must stay bounded.
		rss := vmRSSKB(t, pid)
		if rss < 0 {
			t.Fatalf("daemon disappeared during soak")
		}
		if rss > maxRSS {
			maxRSS = rss
		}
		if rss > rssCeilingKB {
			t.Fatalf("daemon RSS = %d kB (%.1f MB), ceiling %d kB — memory leak (DF-027)", rss, float64(rss)/1024, rssCeilingKB)
		}

		// Drive the load legs.
		if cycle%2 == 0 {
			// Attach/detach cycle against a real live PID (the daemon itself).
			attachOut, err := executeCLI(t, newAttachCmd(), "--pid", fmt.Sprintf("%d", pid), "--no-ebpf", "--addr", addr)
			if err != nil {
				t.Fatalf("attach cycle %d: %v\n%s", cycle, err, attachOut)
			}
			sessID := extractSessionID(t, attachOut)
			if _, err := executeCLI(t, newDetachCmd(), sessID); err != nil {
				t.Fatalf("detach cycle %d: %v", cycle, err)
			}
		}

		if cycle%3 == 0 {
			// WS subscriber: connect to the demo stream, read a flow, drop.
			conn, _, err := websocket.DefaultDialer.Dial(
				"ws://"+addr+"/api/v1/ws/sessions/"+demoSessionID, nil)
			if err != nil {
				t.Fatalf("ws dial: %v", err)
			}
			_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			if _, _, err := conn.ReadMessage(); err != nil {
				t.Logf("ws read (before drop): %v", err)
			}
			conn.Close()
			wsConnected = true
		}

		cycle++
		time.Sleep(3 * time.Second)
	}

	_ = wsConnected
	t.Logf("soak complete: %d cycles, RSS start %d kB / max %d kB (%.1f MB), max /health %v",
		cycle, startRSS, maxRSS, float64(maxRSS)/1024, maxHealth)
	if maxRSS > rssCeilingKB {
		t.Fatalf("max RSS %.1f MB exceeds ceiling 500 MB", float64(maxRSS)/1024)
	}

	// --- SIGQUIT must terminate the daemon promptly (no SIGKILL needed).
	// cmd.Wait() in a goroutine: a killed process lingers as a zombie until
	// reaped, so liveness checks via kill(pid, 0) cannot detect the exit.
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	if err := syscall.Kill(pid, syscall.SIGQUIT); err != nil {
		t.Fatalf("SIGQUIT: %v", err)
	}
	var waitErr error
	select {
	case waitErr = <-waitCh:
		// Exited. Go's default SIGQUIT handler dumps goroutines and exits
		// with status 2; a graceful SIGTERM-like path would exit 0.
		t.Logf("daemon exited after SIGQUIT: %v", waitErr)
	case <-time.After(15 * time.Second):
		t.Fatalf("daemon did not exit within 15s of SIGQUIT — still needs SIGKILL")
	}
	daemonExited = true
	_ = waitErr

	// Port must be freed (daemon really exited, not wedged).
	if _, err := net.Listen("tcp", addr); err != nil {
		t.Fatalf("daemon port %s still busy after exit: %v", addr, err)
	}
	t.Logf("SIGQUIT terminated the daemon; port %s freed", addr)
}
