package messenger

type Message struct {
	Sender string
	Text   string
}

type IMessenger interface {
	Read() (Message, error)
	Write(text string) error
}
