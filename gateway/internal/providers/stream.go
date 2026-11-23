package providers

// StreamReport is the text so far of a reply an agent is writing. Text is the
// whole reply so far. Done means the reply is closed: the conversation has it,
// or will on the next read.
type StreamReport struct {
	NativeID  string
	MessageID string
	Text      string
	Done      bool
}

// StreamReporter is a runtime that reports replies while they are written,
// for apps showing them live (GET /v1/events).
type StreamReporter interface {
	SetStreamListener(func(StreamReport))
}
