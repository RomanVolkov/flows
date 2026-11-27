package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

var agyConversationID = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,128}$`)

// The status line payload may contain private metadata. Persist only its
// validated conversation ID inside the attempt's private directory.
func recordAgyUsageLink() {
	dir := os.Getenv("FLOWS_AGY_USAGE_DIR")
	if !filepath.IsAbs(dir) {
		return
	}
	var state struct {
		Conversation string `json:"conversation_id"`
		Session      string `json:"session_id"`
	}
	if json.NewDecoder(io.LimitReader(os.Stdin, 1024*1024)).Decode(&state) != nil {
		return
	}
	if state.Conversation == "" {
		state.Conversation = state.Session
	}
	if !agyConversationID.MatchString(state.Conversation) {
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, state.Conversation), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		_ = file.Close()
	}
}
