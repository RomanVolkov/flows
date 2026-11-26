// Package localapi defines the daemon's HTTP API over a private Unix socket.
package localapi

const (
	ProtocolVersion = 1
	MaxBodyBytes    = 1 << 20
)

type Version struct {
	Protocol int    `json:"protocol"`
	Build    string `json:"build"`
}

type Health struct {
	Status string `json:"status"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }
