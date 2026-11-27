//go:build production

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/localapi"
)

func (a *App) Attach(attempt string) (string, error) {
	return a.attach(attempt, false, false)
}

func (a *App) AttachScreen(attempt string) (string, error) {
	return a.attach(attempt, false, true)
}

func (a *App) OpenPreview(attempt string) (string, error) { return a.attach(attempt, true, true) }

func (a *App) attach(attempt string, preview, screenOnly bool) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c := client.New(a.home.Socket())
	defer c.Close()
	var sessions []localapi.SessionSummary
	if err := c.JSON(ctx, "GET", "/v1/sessions", "", &sessions); err != nil {
		return "", err
	}
	for _, s := range sessions {
		if s.AttemptID != attempt {
			continue
		}
		id := rand.Text()
		conn, err := host.DialAttach(ctx, s.SocketPath, s.SessionID, host.AttachOptions{Preview: preview, ScreenOnly: screenOnly, ClientID: id, Nonce: s.StartNonce})
		if err != nil {
			return "", err
		}
		a.terminals.Store(id, conn)
		return id, nil
	}
	return "", errors.New("this session has no attached host")
}
func (a *App) OpenTerminal(id string) error               { _, err := a.Attach(id); return err }
func (a *App) CloseTerminal(id string)                    { a.Detach(id) }
func (a *App) TermInput(id, data string) error            { return a.WriteTerminal(id, data) }
func (a *App) TermResize(id string, cols, rows int) error { return a.ResizeTerminal(id, cols, rows) }
func (a *App) TermScroll(id string, rows int) error {
	v, ok := a.terminals.Load(id)
	if !ok {
		return errors.New("terminal closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return v.(*host.AttachConn).WriteInput(ctx, []byte(fmt.Sprintf("\x1b[%d%c", abs(rows), map[bool]byte{true: 'S', false: 'T'}[rows > 0])))
}
func (a *App) ScrollScreen(id string, rows int) error {
	v, ok := a.terminals.Load(id)
	if !ok {
		return errors.New("terminal closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return v.(*host.AttachConn).SendControl(ctx, host.ControlMessage{Type: "scroll", Rows: rows})
}
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
func (a *App) ReadTerminal(id string) (string, error) {
	v, ok := a.terminals.Load(id)
	if !ok {
		return "", errors.New("terminal closed")
	}
	frame, err := v.(*host.AttachConn).ReadFrame(context.Background())
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(frame)
	return string(data), err
}

// StreamTerminal pushes ordered frame events after the webview has subscribed.
func (a *App) StreamTerminal(id string) error {
	v, ok := a.terminals.Load(id)
	if !ok {
		return errors.New("terminal closed")
	}
	conn := v.(*host.AttachConn)
	defer a.Detach(id)
	event := "flows:terminal:" + id
	hasScreen := false
	for {
		frame, err := conn.ReadFrame(a.ctx)
		if err != nil {
			return err
		}
		switch frame.Kind {
		case host.FrameScreen:
			hasScreen = true
		case host.FrameOutput:
			if hasScreen {
				continue
			}
		case host.FrameInputOwner, host.FrameExit:
		default:
			continue
		}
		wailsruntime.EventsEmit(a.ctx, event, struct {
			Kind host.FrameKind `json:"kind"`
			Data string         `json:"data"`
		}{frame.Kind, string(frame.Payload)})
		if frame.Kind == host.FrameExit {
			return nil
		}
	}
}

func (a *App) WriteTerminal(id, text string) error {
	v, ok := a.terminals.Load(id)
	if !ok {
		return errors.New("terminal closed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return v.(*host.AttachConn).WriteInput(ctx, []byte(text))
}
func (a *App) ResizeTerminal(id string, cols, rows int) error {
	v, ok := a.terminals.Load(id)
	if !ok {
		return errors.New("terminal closed")
	}
	if cols < 1 || rows < 1 || cols > 1000 || rows > 1000 {
		return fmt.Errorf("invalid terminal size %dx%d", cols, rows)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return v.(*host.AttachConn).SendResize(ctx, uint16(cols), uint16(rows), 0, 0)
}
func (a *App) Detach(id string) {
	if v, ok := a.terminals.LoadAndDelete(id); ok {
		_ = v.(*host.AttachConn).Close()
	}
}

func (a *App) Sessions() (string, error) { return a.Request("sessions", "") }
func (a *App) Screen(node, session string) (string, error) {
	return a.Request("preview", "screen:"+node+"/"+session)
}
func (a *App) Daily(since string) (string, error)        { return a.Request("daily", since) }
func (a *App) Totals(since, node string) (string, error) { return a.Request("usage", since) }
