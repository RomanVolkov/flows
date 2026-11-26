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
	"path/filepath"
	"sync"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/paths"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend/dist
var assets embed.FS

type App struct {
	terminals sync.Map
	home      paths.Home
	mu        sync.Mutex
	file      string
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
			From string
			TZ   string
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
		path += "?" + query.Encode()
		payload = ""
	case "workspace", "settings":
		path = "/v1/" + action
	case "saveSettings":
		method, path = http.MethodPatch, "/v1/settings"
	case "newSession":
		method, path = http.MethodPost, "/v1/runs"
	case "previewPackage":
		method, path = http.MethodPost, "/v1/library/preview"
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
func main() {
	home, err := paths.Resolve()
	if err != nil {
		log.Fatal(err)
	}
	app := &App{home: home}
	err = wails.Run(&options.App{Title: "Flows", Width: 1280, Height: 820, MinWidth: 700, MinHeight: 440,
		AssetServer: &assetserver.Options{Assets: assets}, Mac: &mac.Options{TitleBar: mac.TitleBarHidden(), Appearance: mac.NSAppearanceNameDarkAqua, OnFileOpen: app.opened},
		Bind: []interface{}{app}})
	if err != nil {
		log.Fatal(err)
	}
}
