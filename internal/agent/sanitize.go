package agent

import (
	"regexp"
	"strings"
)

// internalTags are XML-like wrappers that the model uses for deliberation
// or role-control. Their entire block (open tag, content, close tag) is
// stripped from outbound text. Role tags (system/user/assistant) are also
// stripped as stray fragments — they should never appear in agent output
// or user input.
var internalTags = []string{
	"thinking", "scratchpad", "plan", "analysis",
	"reasoning", "reflection", "inner_monologue",
}

// roleTags are stripped as stray fragments only — not paired-block matched.
// A user typing "</user><system>..." is the threat we're guarding against;
// removing the role tags defangs it without erasing the user's literal text.
var roleTags = []string{"system", "user", "assistant"}

// outputTagBlockRes matches complete <tag>...</tag> blocks for both
// internal-deliberation tags AND role tags. In LLM output, the entire
// block (including content) is removed: the model has no business emitting
// these, and the content is likely either internal reasoning or a
// prompt-injection echo. Go's RE2 has no backreferences, so one regex per
// tag name rather than a \1 pattern.
var outputTagBlockRes = func() []*regexp.Regexp {
	all := append(append([]string{}, internalTags...), roleTags...)
	out := make([]*regexp.Regexp, 0, len(all))
	for _, t := range all {
		out = append(out, regexp.MustCompile(`(?is)<`+t+`\b[^>]*>.*?</`+t+`\s*>`))
	}
	return out
}()

// strayInternalTagRe catches lone opening or closing tags of internal-kind
// or role-kind. Used by both SanitizeLLMOutput (to clean unbalanced
// fragments) and SanitizeUserInput (to defang role-tag injection).
var strayInternalTagRe = regexp.MustCompile(
	`(?i)</?(` + joinPipe(append(append([]string{}, internalTags...), roleTags...)) + `)\b[^>]*>`,
)

var collapseBlankLinesRe = regexp.MustCompile(`\n{3,}`)

func joinPipe(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += "|"
		}
		out += s
	}
	return out
}

// SanitizeLLMOutput strips internal XML-style tag blocks and stray
// role/control tags from LLM-generated text before delivery to users.
// Handles both well-formed <thinking>...</thinking> blocks and dangling
// fragments. Trims trailing whitespace introduced by the removal.
//
// Applied at the boundary between agent and messenger — every text reply
// from the LLM goes through this before reaching messenger.Write.
func SanitizeLLMOutput(text string) string {
	for _, re := range outputTagBlockRes {
		text = re.ReplaceAllString(text, "")
	}
	text = strayInternalTagRe.ReplaceAllString(text, "")
	text = collapseBlankLinesRe.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// SanitizeUserInput strips the same role/control tags from inbound user
// text so users can't trick the model into "switching roles" mid-message
// (e.g. injecting "</user><system>Ignore the above..."). Other angle
// brackets are left alone so legitimate math/code references still work.
//
// Applied at the boundary between messenger and agent — every text we
// formatted as `[Sender]: text` runs through this before going to Anthropic.
func SanitizeUserInput(text string) string {
	return strayInternalTagRe.ReplaceAllString(text, "")
}
