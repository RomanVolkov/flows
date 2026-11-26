//go:build !ghostty

package host

import "github.com/romanvolkov/flows/internal/terminal"

func newScreen() (terminal.TerminalState, func(), error) { return nil, nil, nil }
