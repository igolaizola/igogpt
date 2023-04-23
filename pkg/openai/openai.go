package openai

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/PullRequestInc/go-gpt3"
	"github.com/igolaizola/igogpt/internal/memory"
	"github.com/igolaizola/igogpt/internal/ratelimit"
)

type Client struct {
	gpt3.Client
	rateLimit ratelimit.Lock
	maxTokens int
}

// New returns a new Client.
func New(key string, wait time.Duration, maxTokens int) *Client {
	// Configure rate limit
	if wait == 0 {
		wait = 5 * time.Second
	}
	rateLimit := ratelimit.New(wait)

	client := gpt3.NewClient(key, gpt3.WithTimeout(5*time.Minute))
	return &Client{
		Client:    client,
		rateLimit: rateLimit,
		maxTokens: maxTokens,
	}
}

// Chat creates a new chat session.
func (c *Client) Chat(ctx context.Context, model, role string, mem memory.Memory) io.ReadWriter {
	ctx, cancel := context.WithCancel(ctx)
	rd, wr := io.Pipe()
	return &rw{
		client:     c,
		ctx:        ctx,
		cancel:     cancel,
		model:      model,
		role:       role,
		memory:     mem,
		pipeReader: rd,
		pipeWriter: wr,
		rateLimit:  c.rateLimit,
	}
}

type rw struct {
	client     *Client
	ctx        context.Context
	cancel     context.CancelFunc
	model      string
	role       string
	memory     memory.Memory
	pipeReader *io.PipeReader
	pipeWriter *io.PipeWriter
	rateLimit  ratelimit.Lock
}

// Read reads from the chat.
func (r *rw) Read(b []byte) (n int, err error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	return r.pipeReader.Read(b)
}

// Write writes to the chat.
func (r *rw) Write(b []byte) (n int, err error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	if err := r.memory.Add(memory.Message{
		Role:    r.role,
		Content: string(b),
	}); err != nil {
		return 0, fmt.Errorf("openai: couldn't add message to memory: %w", err)
	}

	// Sum memory
	sum, err := r.memory.Sum()
	if err != nil {
		return 0, fmt.Errorf("openai: couldn't sum memory: %w", err)
	}
	messages := fromMemory(sum)

	// Rate limit requests
	unlock := r.rateLimit.Lock(r.ctx)
	defer unlock()

	completion, err := r.client.ChatCompletion(r.ctx, gpt3.ChatCompletionRequest{
		Model:     r.model,
		Messages:  messages,
		MaxTokens: r.client.maxTokens,
	})
	if err != nil {
		return 0, fmt.Errorf("couldn't generate completion: %w", err)
	}
	if len(completion.Choices) == 0 {
		return 0, fmt.Errorf("no choices")
	}
	response := completion.Choices[0].Message.Content
	log.Printf("openai: request tokens %d", completion.Usage.TotalTokens)

	// Add response to memory
	if err := r.memory.Add(memory.Message{
		Role:    "assistant",
		Content: response,
	}); err != nil {
		return 0, fmt.Errorf("openai: couldn't add message to memory: %w", err)
	}

	// Write response to pipe
	go func() {
		response := response + "\n"
		if _, err := r.pipeWriter.Write([]byte(response)); err != nil {
			log.Println(fmt.Errorf("openai: failed to write to pipe: %w", err))
		}
	}()
	return len(b), nil
}

// Close closes the chat.
func (r *rw) Close() error {
	r.cancel()
	return r.pipeReader.Close()
}

func fromMemory(input []memory.Message) []gpt3.ChatCompletionRequestMessage {
	var output []gpt3.ChatCompletionRequestMessage
	for _, m := range input {
		output = append(output, gpt3.ChatCompletionRequestMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}
	return output
}
