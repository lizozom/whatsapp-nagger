package messenger

type Message struct {
	Sender string
	Text   string
}

// Mention represents a @mention to include in a message.
type Mention struct {
	Phone string // international format without +, e.g. "972501234567"
	Name  string // display name used in the text, e.g. "Liza"
}

type IMessenger interface {
	Read() (Message, error)
	Write(text string) error
	WriteWithMentions(text string, mentions []Mention) error
}
