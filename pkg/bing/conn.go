package bing

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/igolaizola/igogpt/internal/ratelimit"
	"github.com/pavel-one/EdgeGPT-Go/responses"
)

const (
	styleCreative = "h3relaxedimg"
	styleBalanced = "galileo"
	stylePrecise  = "h3precise"
	delimiterByte = uint8(30)
	delimiter     = "\x1e"
)

type conn struct {
	ctx          context.Context
	cancel       context.CancelFunc
	ws           *websocket.Conn
	conversation *Conversation
	invocationID int
	lck          sync.Mutex
	pipeReader   *io.PipeReader
	pipeWriter   *io.PipeWriter
	rateLimit    ratelimit.Lock
}

// Read reads from the chat.
func (c *conn) Read(b []byte) (n int, err error) {
	if c.ctx.Err() != nil {
		return 0, c.ctx.Err()
	}
	return c.pipeReader.Read(b)
}

// Write writes to the chat.
func (c *conn) Write(b []byte) (n int, err error) {
	message := string(b)
	if len(message) > 2000 {
		return 0, fmt.Errorf("bing: message very long, max: %d", 2000)
	}

	// Rate limit requests
	unlock := c.rateLimit.Lock(c.ctx)
	defer unlock()

	m, err := c.send(message)
	if err != nil {
		return 0, err
	}

	go m.Worker()

	for range m.Chan {
		if m.Final {
			break
		}
	}

	go func() {
		if _, err := c.pipeWriter.Write([]byte(m.Answer.GetAnswer())); err != nil {
			log.Println(fmt.Errorf("bing: failed to write to pipe: %w", err))
		}
	}()
	return len(b), nil
}

func (c *conn) send(message string) (*responses.MessageWrapper, error) {
	c.lck.Lock()

	m, err := json.Marshal(c.getRequest(message))
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't marshal request: %w", err)
	}

	m = append(m, delimiterByte)

	if err := c.ws.WriteMessage(websocket.TextMessage, m); err != nil {
		return nil, fmt.Errorf("bing: couldn't write websocket message: %w", err)
	}

	return responses.NewMessageWrapper(message, &c.lck, c.ws), nil
}

// getRequest generate struct for new request websocket
func (c *conn) getRequest(message string) map[string]any {
	rnd := make([]byte, 16)
	_, _ = rand.Read(rnd)
	traceID := hex.EncodeToString(rnd)
	m := map[string]any{
		"invocationId": string(rune(c.invocationID)),
		"target":       "chat",
		"type":         4,
		"arguments": []map[string]any{
			{
				"source": "cib",
				"optionsSets": []string{
					"nlu_direct_response_filter",
					"deepleo",
					"disable_emoji_spoken_text",
					"responsible_ai_policy_235",
					"enablemm",
					// TODO: make it configurable
					styleBalanced,
					"dtappid",
					"cricinfo",
					"cricinfov2",
					"dv3sugg",
				},
				"sliceIds": []string{
					"222dtappid",
					"225cricinfo",
					"224locals0",
				},
				"traceId":          traceID,
				"isStartOfSession": c.invocationID == 0,
				"message": map[string]any{
					"author":      "user",
					"inputMethod": "Keyboard",
					"text":        message,
					"messageType": "Chat",
				},
				"conversationSignature": c.conversation.ConversationSignature,
				"participant": map[string]any{
					"id": c.conversation.ClientId,
				},
				"conversationId": c.conversation.ConversationId,
			},
		},
	}
	c.invocationID++

	return m
}
