package localapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/paths"
)

func homeForTest(t *testing.T) paths.Home {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "flows-api-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	return paths.Home{
		Config:  filepath.Join(root, "config"),
		State:   filepath.Join(root, "state"),
		Cache:   filepath.Join(root, "cache"),
		Logs:    filepath.Join(root, "logs"),
		Runtime: filepath.Join(root, "runtime"),
	}
}

func runDaemon(ctx context.Context, home paths.Home) chan error {
	done := make(chan error, 1)
	go func() { done <- localapi.Run(ctx, home, "test-build") }()
	return done
}

func connectRunning(t *testing.T, ctx context.Context, home paths.Home) *client.Client {
	t.Helper()
	configured := client.New(home.Socket())
	t.Cleanup(configured.Close)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := configured.Version(ctx); err == nil {
			return configured
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for daemon: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForExit(t *testing.T, ctx context.Context, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatalf("wait for daemon exit: %v", ctx.Err())
	}
}

func TestDaemonReplacesStaleSocketAndRestarts(t *testing.T) {
	home := homeForTest(t)
	if err := home.Init(); err != nil {
		t.Fatal(err)
	}
	stale, err := net.ListenUnix("unix", &net.UnixAddr{Name: home.Socket(), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	stale.SetUnlinkOnClose(false)
	stale.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for range 2 {
		done := runDaemon(ctx, home)
		c := connectRunning(t, ctx, home)
		if health, err := c.Health(ctx); err != nil || health.Status != "ready" {
			t.Fatalf("health = %+v, error = %v", health, err)
		}
		if info, err := os.Lstat(home.Socket()); err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("unexpected socket permissions: %v, %v", info, err)
		}
		if err := localapi.Run(ctx, home, "second"); !errors.Is(err, localapi.ErrLocked) {
			t.Fatalf("starting a second daemon: %v", err)
		}
		if err := c.Shutdown(ctx); err != nil {
			t.Fatal(err)
		}
		waitForExit(t, ctx, done)
		if _, err := os.Lstat(home.Socket()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("socket remains after shutdown: %v", err)
		}
	}
}

func TestConcurrentClientsStartDaemonOnce(t *testing.T) {
	home := homeForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan error, 1)
	var starts atomic.Int32
	var clients sync.WaitGroup
	for range 8 {
		clients.Go(func() {
			c, err := client.Connect(ctx, home, func(_ context.Context, h paths.Home) error {
				starts.Add(1)
				go func() { done <- localapi.Run(ctx, h, "test-build") }()
				return nil
			})
			if err != nil {
				t.Errorf("connect: %v", err)
				return
			}
			c.Close()
		})
	}
	clients.Wait()
	if starts.Load() != 1 {
		t.Fatalf("started %d daemons", starts.Load())
	}
	c := connectRunning(t, ctx, home)
	if err := c.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitForExit(t, ctx, done)
}

func TestRejectInsecureRuntimeAndOccupiedSocket(t *testing.T) {
	home := homeForTest(t)
	if err := os.Mkdir(home.Runtime, 0755); err != nil {
		t.Fatal(err)
	}
	if err := localapi.Run(context.Background(), home, "test-build"); err == nil {
		t.Fatal("accepted a public runtime directory")
	}
	if err := os.Chmod(home.Runtime, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home.Socket(), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := localapi.Run(context.Background(), home, "test-build"); err == nil {
		t.Fatal("accepted a regular file as a stale socket")
	}
	if data, err := os.ReadFile(home.Socket()); err != nil || string(data) != "keep" {
		t.Fatalf("occupied socket path changed: %q, %v", data, err)
	}
}

func TestConnectReportsIncompatibleVersion(t *testing.T) {
	home := homeForTest(t)
	if err := home.InitRuntime(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: home.Socket(), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(localapi.Version{Protocol: localapi.ProtocolVersion + 1})
	})}
	t.Cleanup(func() { server.Close() })
	go server.Serve(listener)
	_, err = client.Connect(context.Background(), home, func(context.Context, paths.Home) error {
		t.Error("started another service on version mismatch")
		return nil
	})
	var apiErr *localapi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "incompatible_version" {
		t.Fatalf("expected incompatible version, got %v", err)
	}
}

func TestHTTPBodyBoundAndTypedErrors(t *testing.T) {
	home := homeForTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := runDaemon(ctx, home)
	configured := connectRunning(t, ctx, home)
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", home.Socket())
	}}
	defer transport.CloseIdleConnections()
	raw := &http.Client{Transport: transport}
	for _, tc := range []struct {
		method, route string
		body          io.Reader
		status        int
		code          string
	}{
		{http.MethodGet, "/not-found", nil, 404, "not_found"},
		{http.MethodPut, "/v1/health", nil, 405, "method_not_allowed"},
		{http.MethodPost, "/v1/shutdown", strings.NewReader("unexpected"), 400, "invalid_body"},
		{http.MethodPost, "/v1/shutdown", strings.NewReader(strings.Repeat("x", localapi.MaxBodyBytes+1)), 413, "body_too_large"},
	} {
		req, err := http.NewRequestWithContext(ctx, tc.method, "http://flows.local"+tc.route, tc.body)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := raw.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var apiErr localapi.Error
		err = json.NewDecoder(resp.Body).Decode(&apiErr)
		resp.Body.Close()
		if err != nil || resp.StatusCode != tc.status || apiErr.Code != tc.code {
			t.Fatalf("%s %s = %d %+v, %v", tc.method, tc.route, resp.StatusCode, apiErr, err)
		}
	}
	if err := configured.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	waitForExit(t, ctx, done)
}
