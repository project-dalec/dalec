package gitservices

// ServerEvent represents an event sent from a git server container to the test client.
// Events are sent as newline-delimited JSON on stdout.
type ServerEvent struct {
	// Type indicates the kind of event
	Type string `json:"type" yaml:"type"`

	// Ready is sent when the server is ready to accept connections.
	// It includes the IP address and port the server is listening on.
	Ready *ReadyEvent `json:"ready,omitempty" yaml:"ready,omitempty"`

	// Error is sent when an error occurs.
	Error *ErrorEvent `json:"error,omitempty" yaml:"error,omitempty"`

	// Log is sent for informational messages.
	Log *LogEvent `json:"log,omitempty" yaml:"log,omitempty"`
}

// ReadyEvent indicates the server is ready to accept connections.
type ReadyEvent struct {
	// IP is the IP address the server is bound to (usually the container's IP).
	IP string `json:"ip" yaml:"ip"`
	// Port is the port the server is listening on.
	Port string `json:"port" yaml:"port"`
}

// ErrorEvent indicates an error occurred.
type ErrorEvent struct {
	Message string `json:"message" yaml:"message"`
}

// LogEvent is an informational log message.
type LogEvent struct {
	Message string `json:"message" yaml:"message"`
}

// Event type constants
const (
	EventTypeReady = "ready"
	EventTypeError = "error"
	EventTypeLog   = "log"
)
