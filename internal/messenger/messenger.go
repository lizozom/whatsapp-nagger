package messenger

// Message is one inbound message destined for the agent.
type Message struct {
	GroupID string // WhatsApp JID of the source group (or "dev-group" in terminal mode)
	Sender  string
	Text    string
}

// Mention represents a @mention to include in a message.
type Mention struct {
	Phone string // international format without +, e.g. "972501234567"
	Name  string // display name used in the text, e.g. "Alice"
}

// IMessenger is the seam between dev (terminal) and prod (WhatsApp) message I/O.
// All methods are group-aware: Read() reports which group the message came from,
// and Write/WriteWithMentions take an explicit group_id destination.
type IMessenger interface {
	Read() (Message, error)
	Write(groupID, text string) error
	WriteWithMentions(groupID, text string, mentions []Mention) error
}
