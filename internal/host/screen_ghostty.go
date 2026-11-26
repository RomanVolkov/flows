//go:build ghostty

package host

import "github.com/romanvolkov/flows/internal/terminal"

func newScreen() (terminal.TerminalState, func(), error) {
	screen, err := terminal.NewGhosttyState()
	if err != nil {
		return nil, nil, err
	}
	return screen, screen.Close, nil
}
