//go:build production && !(desktop && darwin)

package main

func nativeTerminalSupported() bool { return false }

func openNativeTerminal(*App, string, string, string, string, float64, float64, float64, float64, float64) error {
	return ErrNativeTerminalUnavailable
}

func setNativeTerminalBounds(string, float64, float64, float64, float64) error {
	return ErrNativeTerminalUnavailable
}

func focusNativeTerminal(string) error { return ErrNativeTerminalUnavailable }

func claimNativeTerminalInput(string) error { return ErrNativeTerminalUnavailable }

func closeNativeTerminal(string) {}
