package messenger

import (
	"bufio"
	"strings"
	"testing"
)

func newTestTerminal(input string) *Terminal {
	return &Terminal{scanner: bufio.NewScanner(strings.NewReader(input))}
}

func TestParseValidMessage(t *testing.T) {
	term := newTestTerminal("[Denis]: Fix the sink\n")
	msg, err := term.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.Sender != "Denis" {
		t.Errorf("expected sender 'Denis', got %q", msg.Sender)
	}
	if msg.Text != "Fix the sink" {
		t.Errorf("expected text 'Fix the sink', got %q", msg.Text)
	}
}

func TestParseMessageWithColonInText(t *testing.T) {
	term := newTestTerminal("[Liza]: Note: buy milk\n")
	msg, err := term.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.Sender != "Liza" {
		t.Errorf("expected sender 'Liza', got %q", msg.Sender)
	}
	if msg.Text != "Note: buy milk" {
		t.Errorf("expected text 'Note: buy milk', got %q", msg.Text)
	}
}

func TestParseUnformattedMessage(t *testing.T) {
	term := newTestTerminal("just some text\n")
	msg, err := term.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.Sender != "Unknown" {
		t.Errorf("expected sender 'Unknown', got %q", msg.Sender)
	}
	if msg.Text != "just some text" {
		t.Errorf("expected text 'just some text', got %q", msg.Text)
	}
}

func TestParseEmptyLine(t *testing.T) {
	term := newTestTerminal("\n")
	msg, err := term.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if msg.Sender != "Unknown" {
		t.Errorf("expected sender 'Unknown', got %q", msg.Sender)
	}
	if msg.Text != "" {
		t.Errorf("expected empty text, got %q", msg.Text)
	}
}

func TestReadEOF(t *testing.T) {
	term := newTestTerminal("")
	_, err := term.Read()
	if err == nil {
		t.Fatal("expected error on EOF")
	}
}

func TestReadMultipleMessages(t *testing.T) {
	term := newTestTerminal("[Denis]: First\n[Liza]: Second\n")

	msg1, err := term.Read()
	if err != nil {
		t.Fatalf("Read 1: %v", err)
	}
	if msg1.Sender != "Denis" || msg1.Text != "First" {
		t.Errorf("msg1: got sender=%q text=%q", msg1.Sender, msg1.Text)
	}

	msg2, err := term.Read()
	if err != nil {
		t.Fatalf("Read 2: %v", err)
	}
	if msg2.Sender != "Liza" || msg2.Text != "Second" {
		t.Errorf("msg2: got sender=%q text=%q", msg2.Sender, msg2.Text)
	}
}

func TestParseMalformedBracket(t *testing.T) {
	term := newTestTerminal("[]: some text\n")
	msg, err := term.Read()
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	// "[" at 0, "]:" at index 1 — idx=1 which is not > 1, so falls through
	if msg.Sender != "Unknown" {
		t.Errorf("expected sender 'Unknown' for malformed bracket, got %q", msg.Sender)
	}
}
