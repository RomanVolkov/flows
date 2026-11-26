//go:build production

package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/romanvolkov/flows/internal/client"
	"github.com/romanvolkov/flows/internal/host"
	"github.com/romanvolkov/flows/internal/localapi"
)

func (a *App) Attach(attempt string) (string, error) {
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
		conn, err := host.DialAttach(ctx, s.SocketPath, s.SessionID, host.AttachOptions{ClientID: id, Nonce: s.StartNonce})
		if err != nil {
			return "", err
		}
		a.terminals.Store(id, conn)
		return id, nil
	}
	return "", errors.New("this session has no attached host")
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
