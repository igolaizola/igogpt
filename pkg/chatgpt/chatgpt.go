package chatgpt

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/go-rod/stealth"
	"github.com/igolaizola/igogpt/internal/ratelimit"
)

type Client struct {
	ctx             context.Context
	cancel          context.CancelFunc
	cancelAllocator context.CancelFunc
	rateLimit       ratelimit.Lock
}

// New returns a new Client.
func New(ctx context.Context, wait time.Duration, remote, proxy string, profile bool) (*Client, error) {
	// Configure rate limit
	if wait == 0 {
		wait = 5 * time.Second
	}
	rateLimit := ratelimit.New(wait)

	var cancelAllocator context.CancelFunc
	if remote != "" {
		log.Println("chatgpt: connecting to browser at", remote)
		ctx, cancelAllocator = chromedp.NewRemoteAllocator(ctx, remote)
	} else {
		log.Println("chatgpt: launching browser")
		opts := append(
			chromedp.DefaultExecAllocatorOptions[3:],
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			chromedp.Flag("headless", false),
		)

		if proxy != "" {
			opts = append(opts,
				chromedp.ProxyServer(proxy),
			)
		}

		if profile {
			opts = append(opts,
				// if user-data-dir is set, chrome won't load the default profile,
				// even if it's set to the directory where the default profile is stored.
				// set it to empty to prevent chromedp from setting it to a temp directory.
				chromedp.UserDataDir(""),
				chromedp.Flag("disable-extensions", false),
			)
		}

		ctx, cancelAllocator = chromedp.NewExecAllocator(ctx, opts...)
	}

	// create chrome instance
	ctx, cancel := chromedp.NewContext(
		ctx,
		// chromedp.WithDebugf(log.Printf),
	)

	// Launch stealth plugin
	if err := chromedp.Run(
		ctx,
		chromedp.Evaluate(stealth.JS, nil),
	); err != nil {
		return nil, fmt.Errorf("chatgpt: could not launch stealth plugin: %w", err)
	}

	// disable webdriver
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(cxt context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument("Object.defineProperty(navigator, 'webdriver', { get: () => false, });").Do(cxt)
		if err != nil {
			return err
		}
		return nil
	})); err != nil {
		return nil, fmt.Errorf("could not disable webdriver: %w", err)
	}

	// check if webdriver is disabled
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://intoli.com/blog/not-possible-to-block-chrome-headless/chrome-headless-test.html"),
	); err != nil {
		return nil, fmt.Errorf("could not navigate to test page: %w", err)
	}
	<-time.After(1 * time.Second)

	if err := chromedp.Run(ctx,
		// Load google first to have a sane referer
		chromedp.Navigate("https://www.google.com/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Navigate("https://chat.openai.com/chat"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.WaitVisible("textarea", chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("chatgpt: could not obtain chatgpt data: %w", err)
	}
	return &Client{
		ctx:             ctx,
		cancel:          cancel,
		cancelAllocator: cancelAllocator,
		rateLimit:       rateLimit,
	}, nil
}

// Close closes the client.
func (c *Client) Close() error {
	c.cancel()
	c.cancelAllocator()
	return nil
}

// Chat starts a new chat in a new tab.
func (c *Client) Chat(ctx context.Context, model string) (io.ReadWriteCloser, error) {
	// Create a new tab based on client context
	tabCtx, cancel := chromedp.NewContext(c.ctx)

	// Close the tab when the provided context is done
	go func() {
		<-ctx.Done()
		c.Close()
	}()

	suffix := "model=text-davinci-002-render-sha"
	if model == "gpt-4" {
		suffix = "model=gpt-4"
	}
	if err := chromedp.Run(tabCtx,
		chromedp.Navigate("https://chat.openai.com/?"+suffix),
		chromedp.WaitVisible("textarea", chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("chatgpt: couldn't navigate to url: %w", err)
	}

	// Wait because there could be redirects
	time.Sleep(1 * time.Second)

	// The url might have changed due to redirects
	var url string
	if err := chromedp.Run(tabCtx, chromedp.Location(&url)); err != nil {
		return nil, fmt.Errorf("chatgpt: couldn't get url: %w", err)
	}
	if !strings.Contains(url, suffix) {
		// Navigating to the URL didn't work, try clicking on the model selector

		// Determine which model option to select
		option := 1
		if model == "gpt-4" {
			option = 2
		}

		// Click on model selector
		ctx, cancel := context.WithTimeout(tabCtx, 5*time.Second)
		defer cancel()
		if err := chromedp.Run(ctx,
			chromedp.Click("button.relative.flex", chromedp.ByQuery),
		); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("chatgpt: couldn't click on model selector: %w", err)
		}
		time.Sleep(200 * time.Millisecond)

		// Click on model option
		if err := chromedp.Run(ctx,
			chromedp.Click(fmt.Sprintf("ul li:nth-child(%d)", option), chromedp.ByQuery),
		); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("chatgpt: couldn't click on model option: %w", err)
		}

		// Test if the url is correct, if not, return an error
		var url string
		if err := chromedp.Run(tabCtx, chromedp.Location(&url)); err != nil {
			return nil, fmt.Errorf("chatgpt: couldn't get url: %w", err)
		}
		if !strings.Contains(url, suffix) {
			return nil, fmt.Errorf("chatgpt: couldn't click on model option %s", model)
		}
	}

	rd, wr := io.Pipe()
	r := &rw{
		client:     c,
		ctx:        tabCtx,
		cancel:     cancel,
		pipeReader: rd,
		pipeWriter: wr,
		rateLimit:  c.rateLimit,
	}

	// Rate limit requests
	unlock := r.rateLimit.LockWithDuration(ctx, time.Second)
	defer unlock()

	return r, nil
}

type rw struct {
	client         *Client
	ctx            context.Context
	cancel         context.CancelFunc
	conversationID string
	lastResponse   string
	pipeReader     *io.PipeReader
	pipeWriter     *io.PipeWriter
	rateLimit      ratelimit.Lock
}

// Read reads from the chat.
func (r *rw) Read(b []byte) (n int, err error) {
	if r.ctx.Err() != nil {
		return 0, r.ctx.Err()
	}
	return r.pipeReader.Read(b)
}

type moderation struct {
	Input          string `json:"input"`
	Model          string `json:"model"`
	ConversationID string `json:"conversation_id"`
	MessageID      string `json:"message_id"`
}

type conversation struct {
	Action   string `json:"action"`
	Messages []struct {
		ID     string `json:"id"`
		Author struct {
			Role string `json:"role"`
		} `json:"author"`
		Content struct {
			ContentType string   `json:"content_type"`
			Parts       []string `json:"parts"`
		} `json:"content"`
	} `json:"messages"`
	ParentMessageID string `json:"parent_message_id"`
	Model           string `json:"model"`
	TimezoneOffset  int    `json:"timezone_offset_min"`
	VariantPurpose  string `json:"variant_purpose"`
}

// Write sends a message to the chat.
func (r *rw) Write(b []byte) (n int, err error) {
	// Rate limit requests
	unlock := r.rateLimit.Lock(r.ctx)
	defer unlock()

	msg := strings.TrimSpace(string(b))

	for {
		err := r.sendMessage(r.ctx, msg)
		if errors.Is(err, errTooManyRequests) {
			// Too many requests, wait for 5 minutes and try again
			log.Println("chatgpt: too many requests, waiting for 5 minutes...")
			select {
			case <-time.After(5 * time.Minute):
			case <-r.ctx.Done():
				return 0, r.ctx.Err()
			}
			// Load the page again using the conversation ID
			if err := chromedp.Run(r.ctx,
				chromedp.Navigate("https://chat.openai.com/c/"+r.conversationID),
				chromedp.WaitVisible("textarea", chromedp.ByQuery),
			); err != nil {
				return 0, fmt.Errorf("chatgpt: couldn't navigate to conversation url: %w", err)
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		break
	}
	go func() {
		response := r.lastResponse + "\n"
		if _, err := r.pipeWriter.Write([]byte(response)); err != nil {
			log.Printf("chatgpt: could not write to pipe: %v", err)
		}
	}()
	return len(b), nil
}

var errTooManyRequests = errors.New("chatgpt: too many requests")

func (r *rw) sendMessage(ctx context.Context, msg string) error {
	// Send the message
	for {
		ctx, cancel := context.WithTimeout(r.ctx, 10*time.Second)
		if err := chromedp.Run(ctx,
			// Update the textarea value with the message
			chromedp.WaitVisible("textarea", chromedp.ByQuery),
			chromedp.SetValue("textarea", msg, chromedp.ByQuery),
		); err != nil {
			log.Println(fmt.Errorf("chatgpt: couldn't type message: %w", err))
			cancel()
			log.Println("chatgpt: waiting for message to be typed...", msg)
			continue
		}
		cancel()
		break
	}

	// Obtain the value of the textarea to check if the message was typed
	for {
		var textarea string
		if err := chromedp.Run(r.ctx,
			chromedp.Value("textarea", &textarea, chromedp.ByQuery),
		); err != nil {
			return fmt.Errorf("chatgpt: couldn't obtain textarea value: %w", err)
		}
		if strings.TrimSpace(textarea) == strings.TrimSpace(msg) {
			break
		}
		log.Println("chatgpt: waiting for textarea to be updated...")
		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}

	// Obtain response from combination of conversation and moderation requests
	var lck sync.Mutex
	var conv *conversation
	wait, done := context.WithCancel(r.ctx)
	defer done()
	chromedp.ListenTarget(
		wait,
		func(ev interface{}) {
			switch e := ev.(type) {
			case *network.EventResponseReceived:
				switch e.Response.URL {
				case "https://chat.openai.com/backend-api/conversation":
					if e.Response.Status == 429 {
						// TODO: handle rate limit
						// We should detect this and retry after a while
						log.Println("chatgpt: rate limited detected")
						return
					}
				default:
					return
				}
			case *network.EventRequestWillBeSent:
				switch e.Request.URL {
				case "https://chat.openai.com/backend-api/conversation":
					lck.Lock()
					defer lck.Unlock()
					if len(e.Request.PostDataEntries) == 0 {
						return
					}
					v, err := base64.StdEncoding.DecodeString(e.Request.PostDataEntries[0].Bytes)
					if err != nil {
						return
					}
					var c conversation
					if err := json.Unmarshal(v, &c); err != nil {
						return
					}
					if len(c.Messages) == 0 || c.Messages[0].ID == "" {
						log.Println("chatgpt: messsage id not found", string(v))
						return
					}
					if len(c.Messages[0].Content.Parts) == 0 {
						log.Println("chatgpt: message content not found", string(v))
						return
					}
					convMsg := c.Messages[0].Content.Parts[0]
					if strings.TrimSpace(convMsg) != strings.TrimSpace(msg) {
						// Skip mismatched messages
						return
					}
					conv = &c
				case "https://chat.openai.com/backend-api/moderations":
					lck.Lock()
					defer lck.Unlock()
					if len(e.Request.PostDataEntries) == 0 {
						return
					}
					v, err := base64.StdEncoding.DecodeString(e.Request.PostDataEntries[0].Bytes)
					if err != nil {
						return
					}
					var m moderation
					if err := json.Unmarshal(v, &m); err != nil {
						return
					}
					if conv == nil {
						log.Printf("chatgpt: moderation received before conversation: %s\n", string(v))
						return
					}
					if m.MessageID == conv.Messages[0].ID {
						return
					}
					prefix := fmt.Sprintf("%s\n%s\n\n", r.lastResponse, conv.Messages[0].Content.Parts[0])
					if !strings.HasPrefix(m.Input, prefix) {
						return
					}
					if r.conversationID == "" {
						r.conversationID = m.ConversationID
					}
					r.lastResponse = strings.TrimPrefix(m.Input, prefix)
					done()
				default:
					return
				}
			}
		},
	)

	// Click on the send button
	d := time.Duration(200+rand.Intn(200)) * time.Millisecond
	<-time.After(d)
	if err := chromedp.Run(r.ctx,
		chromedp.WaitVisible("textarea + button", chromedp.ByQuery),
		chromedp.Click("textarea", chromedp.ByQuery),
		chromedp.Click("textarea + button", chromedp.ByQuery),
	); err != nil {
		return fmt.Errorf("chatgpt: couldn't click button: %w", err)
	}

	// Wait for the response
	select {
	case <-wait.Done():
		return nil
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

// Close closes the chat.
func (r *rw) Close() error {
	r.cancel()
	return r.pipeReader.Close()
}
