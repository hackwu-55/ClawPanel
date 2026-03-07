package process

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zhaoxinyi02/ClawPanel/internal/config"
)

func TestGatewayListening(t *testing.T) {

	openclawDir := newOpenClawDir(t)
	ln, port := listenTCP(t)
	defer ln.Close()
	writeGatewayConfig(t, openclawDir, port)

	mgr := NewManager(&config.Config{OpenClawDir: openclawDir})
	mgr.gatewayProbe = listeningProbe(port)
	if !mgr.GatewayListening() {
		t.Fatalf("expected GatewayListening to detect active gateway port %d", port)
	}
}

func TestGatewayListeningFalseWhenPortClosed(t *testing.T) {

	openclawDir := newOpenClawDir(t)
	ln, port := listenTCP(t)
	_ = ln.Close()
	writeGatewayConfig(t, openclawDir, port)

	mgr := NewManager(&config.Config{OpenClawDir: openclawDir})
	mgr.gatewayProbe = listeningProbe(port)
	if mgr.GatewayListening() {
		t.Fatalf("expected GatewayListening to be false once port %d is closed", port)
	}
}

func TestGatewayListeningIgnoresNonOpenClawListener(t *testing.T) {

	openclawDir := newOpenClawDir(t)
	ln, port := listenTCP(t)
	defer ln.Close()
	writeGatewayConfig(t, openclawDir, port)

	mgr := NewManager(&config.Config{OpenClawDir: openclawDir})
	mgr.gatewayProbe = func(_ string, _ string) bool { return false }
	if mgr.GatewayListening() {
		t.Fatalf("expected GatewayListening to ignore non-OpenClaw listener on port %d", port)
	}
}

func TestGetStatusReportsExternallyManagedGateway(t *testing.T) {

	openclawDir := newOpenClawDir(t)
	ln, port := listenTCP(t)
	defer ln.Close()
	writeGatewayConfig(t, openclawDir, port)

	mgr := NewManager(&config.Config{OpenClawDir: openclawDir})
	mgr.gatewayProbe = listeningProbe(port)
	status := mgr.GetStatus()
	if !status.Running {
		t.Fatalf("expected external gateway to be reported as running")
	}
	if !status.ManagedExternally {
		t.Fatalf("expected external gateway to be marked as managed externally")
	}
}

func TestStartRejectsExternallyManagedGateway(t *testing.T) {

	openclawDir := newOpenClawDir(t)
	ln, port := listenTCP(t)
	defer ln.Close()
	writeGatewayConfig(t, openclawDir, port)

	mgr := NewManager(&config.Config{OpenClawDir: openclawDir})
	mgr.gatewayProbe = listeningProbe(port)
	err := mgr.Start()
	if err == nil || !strings.Contains(err.Error(), "外部进程管理") {
		t.Fatalf("expected Start to reject externally managed gateway, got %v", err)
	}
}

func TestStopRejectsDaemonizedGateway(t *testing.T) {

	mgr := NewManager(&config.Config{})
	mgr.status = Status{Running: true}
	mgr.daemonized = true

	err := mgr.Stop()
	if err == nil || !strings.Contains(err.Error(), "daemon fork 模式") {
		t.Fatalf("expected Stop to reject daemonized gateway, got %v", err)
	}
}

func TestStartRejectsOccupiedNonOpenClawPort(t *testing.T) {

	openclawDir := newOpenClawDir(t)
	ln, port := listenTCP(t)
	defer ln.Close()
	writeGatewayConfig(t, openclawDir, port)

	mgr := NewManager(&config.Config{OpenClawDir: openclawDir})
	mgr.gatewayProbe = func(_ string, _ string) bool { return false }
	err := mgr.Start()
	if err == nil || !strings.Contains(err.Error(), "已被其他本地服务占用") {
		t.Fatalf("expected Start to reject occupied non-OpenClaw port, got %v", err)
	}
}

func newOpenClawDir(t *testing.T) string {
	t.Helper()
	openclawDir := filepath.Join(t.TempDir(), ".openclaw")
	if err := os.MkdirAll(openclawDir, 0755); err != nil {
		t.Fatalf("mkdir openclaw dir: %v", err)
	}
	return openclawDir
}

func writeGatewayConfig(t *testing.T, openclawDir string, port int) {
	t.Helper()
	cfgPath := filepath.Join(openclawDir, "openclaw.json")
	if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf(`{"gateway":{"port":%d}}`, port)), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

func listenTCP(t *testing.T) (net.Listener, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	return ln, port
}

func listeningProbe(port int) func(string, string) bool {
	expectedPort := strconv.Itoa(port)
	return func(host, actualPort string) bool {
		if actualPort != expectedPort {
			return false
		}
		if host != "localhost" {
			ip := net.ParseIP(host)
			if ip == nil || !ip.IsLoopback() {
				return false
			}
		}
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, actualPort), 200*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}
}
