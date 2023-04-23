package fixed

import (
	"fmt"

	"github.com/igolaizola/igogpt/internal/memory"
	"github.com/tiktoken-go/tokenizer"
)

type fixedMemory struct {
	keepFirst int
	maxTokens int
	messages  []memory.Message
}

func NewFixedMemory(keepFirst, maxTokens int) *fixedMemory {
	return &fixedMemory{
		keepFirst: keepFirst,
		maxTokens: maxTokens,
	}
}

func (m *fixedMemory) Add(msg memory.Message) error {
	m.messages = append(m.messages, msg)
	return nil
}

func (m *fixedMemory) Sum() ([]memory.Message, error) {
	// Keep first messages
	first := []memory.Message{}
	rest := m.messages
	if m.keepFirst > 0 && len(m.messages) > m.keepFirst {
		first = m.messages[:m.keepFirst]
		rest = m.messages[m.keepFirst:]
	}

	// Count tokens and remove oldest messages if needed
	if m.maxTokens > 0 {
		for {
			tokens, err := Tokens(append(first, rest...))
			if err != nil {
				return nil, fmt.Errorf("openai: couldn't count tokens: %w", err)
			}
			// Leave some tokens for the response
			if tokens+1000 <= m.maxTokens {
				break
			}
			if len(rest) == 1 {
				return nil, fmt.Errorf("openai: prompt too long (%d tokens)", tokens)
			}
			rest = rest[1:]
		}
	}
	return append(first, rest...), nil
}

func Tokens(messages []memory.Message) (int, error) {
	text := ""
	for _, message := range messages {
		text += message.Content + "\n"
	}

	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		return 0, fmt.Errorf("openai: couldn't get tokenizer: %w", err)
	}

	// Encode to obtain the list of tokens
	ids, _, _ := enc.Encode(text)
	tokens := len(ids)

	// Add 8 tokens extra per message
	tokens = tokens + (len(messages) * 8)
	return tokens, nil
}
