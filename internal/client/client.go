// Package client connects the CLI and desktop to the local Flows daemon.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/paths"
)

type Client struct {
	http *http.Client
}

func New(socket string) *Client {
	dialer := net.Dialer{Timeout: time.Second}
	return &Client{http: &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, "unix", socket)
			},
			IdleConnTimeout: 30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}}
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

func (c *Client) Version(ctx context.Context) (localapi.Version, error) {
	var version localapi.Version
	if err := c.request(ctx, http.MethodGet, "/v1/version", &version); err != nil {
		return localapi.Version{}, err
	}
	if version.Protocol != localapi.ProtocolVersion {
		return version, &localapi.Error{
			Code:    "incompatible_version",
			Message: fmt.Sprintf("Flows service protocol %d is incompatible with client protocol %d", version.Protocol, localapi.ProtocolVersion),
		}
	}
	return version, nil
}

func (c *Client) Health(ctx context.Context) (localapi.Health, error) {
	var health localapi.Health
	err := c.request(ctx, http.MethodGet, "/v1/health", &health)
	return health, err
}

func (c *Client) Shutdown(ctx context.Context) error {
	return c.request(ctx, http.MethodPost, "/v1/shutdown", &localapi.Health{})
}

func (c *Client) request(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://flows.local"+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(resp.Body, localapi.MaxBodyBytes))
	if resp.StatusCode != http.StatusOK {
		apiErr := new(localapi.Error)
		if err := decoder.Decode(apiErr); err != nil || apiErr.Code == "" {
			return fmt.Errorf("service returned HTTP %d", resp.StatusCode)
		}
		return apiErr
	}
	return decoder.Decode(out)
}

type Starter func(context.Context, paths.Home) error

// Connect starts the registered LaunchAgent if the socket has no listener.
// A competing client waits for the first caller's launch attempt.
func Connect(ctx context.Context, home paths.Home, start Starter) (*Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	if err := home.InitRuntime(); err != nil {
		return nil, err
	}
	client := New(home.Socket())
	var lock *os.File
	defer func() {
		if lock != nil {
			lock.Close()
		}
	}()
	for {
		_, err := client.Version(ctx)
		if err == nil {
			return client, nil
		}
		if !missingService(err) || start == nil {
			client.Close()
			return nil, err
		}
		if lock == nil {
			lock, err = localapi.TryLock(filepath.Join(home.Runtime, "start.lock"))
			switch {
			case err == nil:
				// A starter may have completed between the probe and lock.
				if _, err := client.Version(ctx); err == nil {
					return client, nil
				} else if !missingService(err) {
					return nil, err
				}
				if err := start(ctx, home); err != nil {
					return nil, err
				}
			case !errors.Is(err, localapi.ErrLocked):
				return nil, err
			}
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			client.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func missingService(err error) bool {
	return errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED)
}

func StartInstalled(ctx context.Context, home paths.Home) error {
	target := "gui/" + strconv.Itoa(os.Getuid()) + "/" + home.ServiceLabel()
	output, err := exec.CommandContext(ctx, "/bin/launchctl", "kickstart", target).CombinedOutput()
	if err != nil {
		return &localapi.Error{Code: "service_unavailable", Message: fmt.Sprintf("start %s: %v: %s", target, err, output)}
	}
	return nil
}
