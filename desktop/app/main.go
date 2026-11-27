//go:build production

// Package main hosts the Flows workspace in the system webview.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/localapi"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/romanvolkov/flows/internal/store"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed all:frontend/dist
var assets embed.FS

type App struct {
	terminals       sync.Map
	nativeTerminals sync.Map
	home            paths.Home
	ctx       context.Context
	mu        sync.Mutex
	file      string
	followMu  sync.Mutex
	unfollow  context.CancelFunc
	followID  uint64
}

type workspaceState struct {
	Workspace store.Workspace           `json:"workspace"`
	Sessions  []localapi.SessionSummary `json:"sessions"`
}

type daemonConnection struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func (a *App) opened(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.file = filepath.Base(path)
}
func (a *App) OpenedFile() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	name := a.file
	a.file = ""
	return name
}
func (a *App) Request(action, payload string) (string, error) {
	method, path := http.MethodGet, ""
	switch action {
	case "usage", "daily":
		var filter struct {
			From  string `json:"from"`
			TZ    string `json:"tz"`
			Query string `json:"query"`
		}
		if payload != "" {
			if err := json.Unmarshal([]byte(payload), &filter); err != nil {
				return "", err
			}
		}
		path = "/v1/usage"
		if action == "daily" {
			path += "/daily"
		}
		query := url.Values{"from": {filter.From}, "tz": {filter.TZ}}
		parsed, _ := url.ParseQuery(filter.Query)
		for key, values := range parsed {
			for _, value := range values {
				query.Add(key, value)
			}
		}
		path += "?" + query.Encode()
		payload = ""
	case "workspace", "settings", "harnesses", "sessions", "library":
		path = "/v1/" + action
	case "stopSession":
		method, path = http.MethodPost, "/v1/sessions/"+url.PathEscape(payload)+"/stop"
		payload = ""
	case "stopNode":
		method, path = http.MethodPost, "/v1/nodes/"+url.PathEscape(payload)+"/stop"
		payload = ""
	case "removeNode":
		method, path = http.MethodPost, "/v1/nodes/"+url.PathEscape(payload)+"/remove"
		payload = ""
	case "resumeNode":
		method, path = http.MethodPost, "/v1/nodes/"+url.PathEscape(payload)+"/resume"
		payload = ""
	case "saveSettings":
		method, path = http.MethodPatch, "/v1/settings"
	case "newSession":
		method, path = http.MethodPost, "/v1/runs"
	case "previewPackage":
		method, path = http.MethodPost, "/v1/library/preview"
	case "validatePackage":
		method, path = http.MethodPost, "/v1/library/validate"
	case "importPackage":
		method, path = http.MethodPost, "/v1/library/import"
	case "preview":
		if payload == "" || filepath.Base(payload) != payload || payload == "." || payload == ".." {
			return "", errors.New("invalid attempt")
		}
		path = "/v1/preview/" + payload
		payload = ""
	default:
		return "", errors.New("unknown workspace action")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	c, err := client.Connect(ctx, a.home, client.StartInstalled)
	if err != nil {
		return "", err
	}
	defer c.Close()
	var out json.RawMessage
	err = c.JSON(ctx, method, path, payload, &out)
	return string(out), err
}
func (a *App) startup(ctx context.Context) { a.ctx = ctx }

func (a *App) shutdown(context.Context) {
	a.nativeTerminals.Range(func(key, _ any) bool {
		if id, ok := key.(string); ok {
			a.Detach(id)
		}
		return true
	})
	a.terminals.Range(func(key, _ any) bool {
		if id, ok := key.(string); ok {
			a.Detach(id)
		}
		return true
	})
}

// OpenLibrary opens the selected user package folder in Finder.
func (a *App) OpenLibrary(kind string) error {
	var path string
	switch kind {
	case "workflows":
		path = a.home.Workflows()
	case "tools":
		path = a.home.Tools()
	default:
		return errors.New("unknown library folder")
	}
	return exec.Command("open", path).Run()
}

// Follow starts a snapshot and event stream after the web view has subscribed.
func (a *App) Follow() {
	a.followMu.Lock()
	if a.unfollow != nil {
		a.unfollow()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.unfollow = cancel
	a.followID++
	id := a.followID
	a.followMu.Unlock()

	go func() {
		defer func() {
			a.followMu.Lock()
			if a.followID == id {
				a.unfollow = nil
			}
			a.followMu.Unlock()
		}()
		for ctx.Err() == nil {
			c, err := client.Connect(ctx, a.home, client.StartInstalled)
			if err != nil {
				a.connection(false, err)
				if !waitForRetry(ctx) {
					return
				}
				continue
			}
			err = c.Follow(ctx, client.WorkspaceObserver{
				Reset: func(view store.Workspace, _ []string) { a.publishWorkspace(ctx, c, view) },
				Apply: func(store.Event) {
					view, err := c.Workspace(ctx)
					if err != nil {
						a.connection(false, err)
						return
					}
					a.publishWorkspace(ctx, c, view)
				},
				Connected: func(ok bool, err error) { a.connection(ok, err) },
			})
			c.Close()
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				a.connection(false, err)
			}
			if !waitForRetry(ctx) {
				return
			}
		}
	}()
}

func waitForRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (a *App) publishWorkspace(ctx context.Context, c *client.Client, view store.Workspace) {
	var sessions []localapi.SessionSummary
	if err := c.JSON(ctx, http.MethodGet, "/v1/sessions", "", &sessions); err != nil {
		a.connection(false, err)
		return
	}
	wailsruntime.EventsEmit(a.ctx, "flows:snapshot", workspaceState{Workspace: view, Sessions: sessions})
	a.connection(true, nil)
}

func (a *App) connection(ok bool, err error) {
	state := daemonConnection{OK: ok}
	if err != nil {
		state.Error = err.Error()
	}
	wailsruntime.EventsEmit(a.ctx, "flows:connection", state)
}

func (a *App) Clipboard(text string) bool {
	return a.ctx != nil && wailsruntime.ClipboardSetText(a.ctx, text) == nil
}
func runNativeAttach(socketPath, sessionID string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return host.AttachNative(ctx, socketPath, sessionID, os.Stdin, os.Stdout, host.NativeAttachOptionsFromEnv())
}

func main() {
	if len(os.Args) >= 4 && os.Args[1] == "--native-attach" {
		if err := runNativeAttach(os.Args[2], os.Args[3]); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal(err)
		}
		return
	}
	home, err := paths.Resolve()
	if err != nil {
		log.Fatal(err)
	}
	app := &App{home: home}
	err = wails.Run(&options.App{Title: "Flows", Width: 1280, Height: 820, MinWidth: 700, MinHeight: 440,
		BackgroundColour: &options.RGBA{},
		AssetServer:      &assetserver.Options{Assets: assets}, Mac: &mac.Options{TitleBar: mac.TitleBarHidden(), WebviewIsTransparent: true, WindowIsTranslucent: true, Appearance: mac.NSAppearanceNameDarkAqua, OnFileOpen: app.opened}, OnStartup: app.startup, OnShutdown: app.shutdown,
		SingleInstanceLock: &options.SingleInstanceLock{UniqueId: "dev.flows.app", OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.focusWindow() }},
		Bind:               []interface{}{app}})
	if err != nil {
		log.Fatal(err)
	}
}

func (a *App) focusWindow() {
	if a.ctx == nil {
		return
	}
	wailsruntime.WindowUnminimise(a.ctx)
	wailsruntime.WindowShow(a.ctx)
	wailsruntime.Show(a.ctx)
}
