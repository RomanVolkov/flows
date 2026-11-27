//go:build desktop && production && darwin

package main

/*
#cgo CFLAGS: -fobjc-arc
#include <stdlib.h>
#include <stdbool.h>
#include <stdint.h>

bool flowsNativeTerminalOpen(const char *id, const char *exe, const char *socketPath, const char *sessionID, const char *nonce, double x, double y, double width, double height, float fontSize);
bool flowsNativeTerminalSetBounds(const char *id, double x, double y, double width, double height);
bool flowsNativeTerminalFocus(const char *id);
bool flowsNativeTerminalClaimInput(const char *id);
void flowsNativeTerminalClose(const char *id);
*/
import "C"

import (
	"context"
	"errors"
	"os"
	"sync"
	"unsafe"

	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type nativeTerminalEventPayload struct {
	Type     string `json:"type"`
	OwnerID  string `json:"owner_id,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Title    string `json:"title,omitempty"`
	Scrolled uint64 `json:"scrolled,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	IsOwner  bool   `json:"is_owner,omitempty"`
}

var (
	nativeAppsMu sync.RWMutex
	nativeApps   = map[string]*App{}

	nativeEventOnce   sync.Once
	nativeEventMu     sync.Mutex
	nativeEventQueue  []nativeEventItem
	nativeEventNotify = make(chan struct{}, 1)
)

type nativeEventItem struct {
	ctx     context.Context
	channel string
	payload nativeTerminalEventPayload
}

func enqueueNativeTerminalEvent(ctx context.Context, channel string, payload nativeTerminalEventPayload) {
	nativeEventOnce.Do(func() {
		go runNativeTerminalEventDispatcher()
	})
	nativeEventMu.Lock()
	nativeEventQueue = append(nativeEventQueue, nativeEventItem{
		ctx:     ctx,
		channel: channel,
		payload: payload,
	})
	nativeEventMu.Unlock()
	select {
	case nativeEventNotify <- struct{}{}:
	default:
	}
}

func runNativeTerminalEventDispatcher() {
	for range nativeEventNotify {
		for {
			nativeEventMu.Lock()
			if len(nativeEventQueue) == 0 {
				nativeEventMu.Unlock()
				break
			}
			batch := nativeEventQueue
			nativeEventQueue = nil
			nativeEventMu.Unlock()
			for _, item := range batch {
				wailsruntime.EventsEmit(item.ctx, item.channel, item.payload)
			}
		}
	}
}

func nativeTerminalSupported() bool {
	return os.Getenv("FLOWS_TERMINAL_RENDERER") != "canvas"
}

func openNativeTerminal(a *App, id, socketPath, sessionID, nonce string, x, y, width, height, fontSize float64) error {
	if !nativeTerminalSupported() {
		return ErrNativeTerminalUnavailable
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return openNativeTerminalWithExe(a, exe, id, socketPath, sessionID, nonce, x, y, width, height, fontSize)
}

func openNativeTerminalWithExe(a *App, exe, id, socketPath, sessionID, nonce string, x, y, width, height, fontSize float64) error {
	if !nativeTerminalSupported() {
		return ErrNativeTerminalUnavailable
	}
	nativeAppsMu.Lock()
	nativeApps[id] = a
	nativeAppsMu.Unlock()

	cid := C.CString(id)
	cexe := C.CString(exe)
	csock := C.CString(socketPath)
	csess := C.CString(sessionID)
	cnonce := C.CString(nonce)
	defer func() {
		C.free(unsafe.Pointer(cid))
		C.free(unsafe.Pointer(cexe))
		C.free(unsafe.Pointer(csock))
		C.free(unsafe.Pointer(csess))
		C.free(unsafe.Pointer(cnonce))
	}()

	if !bool(C.flowsNativeTerminalOpen(cid, cexe, csock, csess, cnonce, C.double(x), C.double(y), C.double(width), C.double(height), C.float(fontSize))) {
		nativeAppsMu.Lock()
		delete(nativeApps, id)
		nativeAppsMu.Unlock()
		return errors.New("could not open native Ghostty terminal surface")
	}
	return nil
}

func setNativeTerminalBounds(id string, x, y, width, height float64) error {
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	if !bool(C.flowsNativeTerminalSetBounds(cid, C.double(x), C.double(y), C.double(width), C.double(height))) {
		return errors.New("native terminal not found")
	}
	return nil
}

func focusNativeTerminal(id string) error {
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	if !bool(C.flowsNativeTerminalFocus(cid)) {
		return errors.New("native terminal not found")
	}
	return nil
}

func claimNativeTerminalInput(id string) error {
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	if !bool(C.flowsNativeTerminalClaimInput(cid)) {
		return errors.New("native terminal not found")
	}
	return nil
}

func closeNativeTerminal(id string) {
	cid := C.CString(id)
	defer C.free(unsafe.Pointer(cid))
	C.flowsNativeTerminalClose(cid)
	nativeAppsMu.Lock()
	delete(nativeApps, id)
	nativeAppsMu.Unlock()
}

//export flowsNativeTerminalDispatchEvent
func flowsNativeTerminalDispatchEvent(
	cid *C.char,
	ceventType *C.char,
	cownerID *C.char,
	creason *C.char,
	ctitle *C.char,
	scrolled C.uint64_t,
	exitCode C.int,
	isOwner C.bool,
) {
	id := C.GoString(cid)
	nativeAppsMu.RLock()
	app := nativeApps[id]
	nativeAppsMu.RUnlock()
	if app == nil || app.ctx == nil {
		return
	}
	evt := nativeTerminalEventPayload{
		Type:     C.GoString(ceventType),
		Scrolled: uint64(scrolled),
		ExitCode: int(exitCode),
		IsOwner:  bool(isOwner),
	}
	if cownerID != nil {
		evt.OwnerID = C.GoString(cownerID)
	}
	if creason != nil {
		evt.Reason = C.GoString(creason)
	}
	if ctitle != nil {
		evt.Title = C.GoString(ctitle)
	}
	// Dispatch off the AppKit main thread: Wails v2 ExecJS calls [nsscript release]
	// inside ON_MAIN_THREAD on an autoreleased NSString, which double-frees if called
	// while the main thread's autorelease pool drains before the async block runs.
	enqueueNativeTerminalEvent(app.ctx, "flows:terminal-native:"+id, evt)
}
