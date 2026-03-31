package messenger

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Terminal struct {
	scanner *bufio.Scanner
}

func NewTerminal() *Terminal {
	return &Terminal{scanner: bufio.NewScanner(os.Stdin)}
}

func (t *Terminal) Read() (Message, error) {
	fmt.Print("> ")
	if !t.scanner.Scan() {
		if err := t.scanner.Err(); err != nil {
			return Message{}, err
		}
		return Message{}, fmt.Errorf("EOF")
	}

	line := strings.TrimSpace(t.scanner.Text())
	if line == "" {
		return Message{Sender: "Unknown", Text: ""}, nil
	}

	// Parse "[Name]: message" format
	if strings.HasPrefix(line, "[") {
		if idx := strings.Index(line, "]: "); idx > 1 {
			return Message{
				Sender: line[1:idx],
				Text:   line[idx+3:],
			}, nil
		}
	}

	return Message{Sender: "Unknown", Text: line}, nil
}

func (t *Terminal) Write(text string) error {
	fmt.Printf("[Nagger]: %s\n", text)
	return nil
}

func (t *Terminal) WriteWithMentions(text string, mentions []Mention) error {
	return t.Write(text)
}
