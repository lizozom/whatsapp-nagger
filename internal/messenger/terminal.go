package messenger

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// devGroupID is the synthesized GroupID for terminal-mode messages. Every
// inbound terminal Read carries this value, and Writes ignore the explicit
// group_id since terminal mode has only one output channel anyway.
const devGroupID = "dev-group"

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
		return Message{GroupID: devGroupID, Sender: "Unknown", Text: ""}, nil
	}

	// Parse "[Name]: message" format
	if strings.HasPrefix(line, "[") {
		if idx := strings.Index(line, "]: "); idx > 1 {
			return Message{
				GroupID: devGroupID,
				Sender:  line[1:idx],
				Text:    line[idx+3:],
			}, nil
		}
	}

	return Message{GroupID: devGroupID, Sender: "Unknown", Text: line}, nil
}

// Write ignores groupID — terminal mode has a single stdout channel.
func (t *Terminal) Write(groupID, text string) error {
	fmt.Printf("[Nagger]: %s\n", text)
	return nil
}

func (t *Terminal) WriteWithMentions(groupID, text string, mentions []Mention) error {
	return t.Write(groupID, text)
}
