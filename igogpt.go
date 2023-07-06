package igogpt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/igolaizola/igogpt/internal/command"
	"github.com/igolaizola/igogpt/internal/prompt"
	"github.com/igolaizola/igogpt/pkg/bing"
	"github.com/igolaizola/igogpt/pkg/chatgpt"
	"github.com/igolaizola/igogpt/pkg/memory/fixed"
	"github.com/igolaizola/igogpt/pkg/openai"
)

type Config struct {
	AI     string `yaml:"ai"`
	Goal   string `yaml:"goal"`
	Prompt string `yaml:"prompt"`
	Model  string `yaml:"model"`
	Proxy  string `yaml:"proxy"`
	Output string `yaml:"output"`
	LogDir string `yaml:"log-dir"`
	Steps  int    `yaml:"steps"`

	// Bulk parameters
	BulkInput  string `yaml:"bulk-input"`
	BulkOutput string `yaml:"bulk-output"`

	// Google parameters
	GoogleKey string `yaml:"google-key"`
	GoogleCX  string `yaml:"google-cx"`

	// Openai parameters
	OpenaiWait      time.Duration `yaml:"openai-wait"`
	OpenaiKey       string        `yaml:"openai-key"`
	OpenaiMaxTokens int           `yaml:"openai-max-tokens"`

	// Chatgpt parameters
	ChatgptWait   time.Duration `yaml:"chatgpt-wait"`
	ChatgptRemote string        `yaml:"chatgpt-remote"`

	// Bing parameters
	BingWait        time.Duration `yaml:"bing-wait"`
	BingSessionFile string        `yaml:"bing-session"`
	BingSession     bing.Session  `yaml:"-"`
}

func Run(ctx context.Context, action string, cfg *Config) error {
	switch action {
	case "pair":
		return Pair(ctx, cfg)
	case "auto":
		return Auto(ctx, cfg)
	case "chat":
		return Chat(ctx, cfg)
	case "bulk":
		return Bulk(ctx, cfg)
	case "cmd":
		return Cmd(ctx, cfg)
	default:
		return fmt.Errorf("igogpt: unknown action: %s", action)
	}
}

// Chat runs a chat session
func Chat(ctx context.Context, cfg *Config) error {
	var chat io.ReadWriter
	switch cfg.AI {
	case "bing":
		// Create bing client
		bingClient, err := bing.New(cfg.BingWait, &cfg.BingSession, cfg.BingSessionFile, cfg.Proxy)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create bing client: %w", err)
		}
		bingChat, err := bingClient.Chat(ctx)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create bing chat: %w", err)
		}
		chat = bingChat
		defer bingChat.Close()
	case "chatgpt":
		// Create chatgpt client
		client, err := chatgpt.New(ctx, cfg.ChatgptWait, cfg.ChatgptRemote, cfg.Proxy, true)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create chatgpt client: %w", err)
		}
		defer client.Close()
		chat, err = client.Chat(ctx, cfg.Model)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create chatgpt chat: %w", err)
		}
	case "openai":
		// Create openai client
		client := openai.New(cfg.OpenaiKey, cfg.OpenaiWait, cfg.OpenaiMaxTokens)
		chat = client.Chat(ctx, cfg.Model, "user", fixed.NewFixedMemory(0, cfg.OpenaiMaxTokens))
	default:
		return fmt.Errorf("igogpt: invalid ai: %s", cfg.AI)
	}

	err1 := make(chan error)
	err2 := make(chan error)
	go func() {
		if _, err := io.Copy(os.Stdout, chat); err != nil {
			err1 <- fmt.Errorf("igogpt: couldn't copy: %w", err)
		}
	}()
	go func() {
		if _, err := io.Copy(chat, os.Stdin); err != nil {
			err2 <- fmt.Errorf("igogpt: couldn't copy: %w", err)
		}
	}()
	select {
	case <-ctx.Done():
		return nil
	case err := <-err1:
		return err
	case err := <-err2:
		return err
	}
}

// Auto runs auto mode
func Auto(ctx context.Context, cfg *Config) error {
	if cfg.Goal == "" && cfg.Prompt == "" {
		return fmt.Errorf("igogpt: goal or prompt is required")
	}
	prmpt := fmt.Sprintf(prompt.Auto, cfg.Goal)
	if cfg.BingSession.Cookie == "" {
		prmpt = fmt.Sprintf(prompt.AutoNoBing, cfg.Goal)
	}
	if cfg.Prompt != "" {
		prmpt = cfg.Prompt
	}

	if cfg.Output == "" {
		return fmt.Errorf("igogpt: output is required")
	}
	if err := os.MkdirAll(cfg.Output, 0755); err != nil {
		return fmt.Errorf("igogpt: couldn't create output directory: %w", err)
	}

	// Create main chat
	var chat io.ReadWriter
	switch cfg.AI {
	case "bing":
		return fmt.Errorf("igogpt: bing is not supported in auto mode")
	case "chatgpt":
		// Create chatgpt client
		client, err := chatgpt.New(ctx, cfg.ChatgptWait, cfg.ChatgptRemote, cfg.Proxy, true)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create chatgpt client: %w", err)
		}
		defer client.Close()
		chat, err = client.Chat(ctx, cfg.Model)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create chatgpt chat: %w", err)
		}
	case "openai":
		// Create openai client
		client := openai.New(cfg.OpenaiKey, cfg.OpenaiWait, cfg.OpenaiMaxTokens)
		chat = client.Chat(ctx, cfg.Model, "system", fixed.NewFixedMemory(1, cfg.OpenaiMaxTokens))
	default:
		return fmt.Errorf("igogpt: invalid ai: %s", cfg.AI)
	}

	// Set logger
	logger, err := newLogger(chat, cfg.LogDir)
	if err != nil {
		return fmt.Errorf("igogpt: couldn't create logger: %w", err)
	}
	defer logger.Close()
	chat = logger

	// Create bing chat
	var bingChat io.ReadWriter
	bingChat = &notAvailable{}
	if cfg.BingSession.Cookie != "" {
		bingClient, err := bing.New(cfg.BingWait, &cfg.BingSession, cfg.BingSessionFile, cfg.Proxy)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create bing client: %w", err)
		}
		chat, err := bingClient.Chat(ctx)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create bing chat: %w", err)
		}
		defer chat.Close()
		bingChat = chat
	} else {
		log.Println("no bing session provided, skipping bing")
	}

	// Command runner
	ctx, exit := context.WithCancel(ctx)
	runner := command.New(&command.Config{
		Exit:      exit,
		Output:    cfg.Output,
		Bing:      bingChat,
		GoogleKey: cfg.GoogleKey,
		GoogleCX:  cfg.GoogleCX,
	})

	send := prmpt
	log.Println("starting auto mode")
	steps := 0
	for {
		// Check context
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// Check steps
		if cfg.Steps > 0 && steps >= cfg.Steps {
			return nil
		}
		steps++

		// Write to chat
		if _, err := chat.Write([]byte(send)); err != nil {
			return fmt.Errorf("igogpt: couldn't write message to chatgpt: %w", err)
		}

		// Read from chat
		buf := make([]byte, 1024*64)
		n, err := chat.Read(buf)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't read message from chatgpt: %w", err)
		}
		recv := string(buf[:n])

		// Run commands
		result := runner.Run(ctx, recv)

		// Marshal data
		js, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			send = err.Error()
			continue
		}
		send = string(js)
	}
}

// Pair connects two chats
func Pair(ctx context.Context, cfg *Config) error {
	if cfg.Goal == "" && cfg.Prompt == "" {
		return fmt.Errorf("igogpt: goal or prompt is required")
	}
	prmpt := fmt.Sprintf(prompt.Pair, cfg.Goal)
	if cfg.Prompt != "" {
		prmpt = cfg.Prompt
	}

	// Create chatgpt client
	cgptClient, err := chatgpt.New(ctx, cfg.ChatgptWait, cfg.ChatgptRemote, cfg.Proxy, true)
	if err != nil {
		return fmt.Errorf("igogpt: couldn't create chatgpt client: %w", err)
	}
	defer cgptClient.Close()

	// Create first chat
	chat, err := cgptClient.Chat(ctx, cfg.Model)
	if err != nil {
		return fmt.Errorf("igogpt: couldn't create chatgpt chat: %w", err)
	}
	defer chat.Close()

	// Set exit conditon checker
	exit := &exitChecker{
		ReadWriter: chat,
		exit:       "exit-igogpt",
	}

	// Set logger
	logger, err := newLogger(exit, cfg.LogDir)
	if err != nil {
		return fmt.Errorf("igogpt: couldn't create logger: %w", err)
	}
	defer logger.Close()
	chat1 := logger

	// Create second chat
	chat2, err := cgptClient.Chat(ctx, cfg.Model)
	if err != nil {
		return fmt.Errorf("igogpt: couldn't create chatgpt chat: %w", err)
	}
	defer chat2.Close()

	if _, err := chat1.Write([]byte(prmpt)); err != nil {
		return fmt.Errorf("igogpt: couldn't write to chat1: %w", err)
	}
	err1 := make(chan error)
	err2 := make(chan error)
	go func() {
		if _, err := io.Copy(chat1, chat2); err != nil {
			err1 <- fmt.Errorf("igogpt: couldn't copy: %w", err)
		}
	}()
	go func() {
		if _, err := io.Copy(chat2, chat1); err != nil {
			err2 <- fmt.Errorf("igogpt: couldn't copy: %w", err)
		}
	}()
	select {
	case <-ctx.Done():
		return nil
	case err := <-err1:
		return err
	case err := <-err2:
		return err
	}
}

type BulkOutput [][]inOut

type inOut struct {
	In  string `json:"in"`
	Out string `json:"out"`
}

func Bulk(ctx context.Context, cfg *Config) error {
	if cfg.BulkInput == "" {
		return fmt.Errorf("igogpt: bulk input is required")
	}
	if cfg.BulkOutput == "" {
		return fmt.Errorf("igogpt: bulk output is required")
	}

	// Read bulk input file
	b, err := os.ReadFile(cfg.BulkInput)
	if err != nil {
		return fmt.Errorf("igogpt: couldn't read bulk input file: %w", err)
	}
	var inputs [][]string

	if filepath.Ext(cfg.BulkInput) == ".json" {
		var list []any
		if err := json.Unmarshal(b, &list); err != nil {
			return fmt.Errorf("igogpt: couldn't unmarshal bulk input file: %w", err)
		}
		if len(list) == 0 {
			return fmt.Errorf("igogpt: no inputs found in bulk input file")
		}
		for _, elem := range list {
			// Check if input is a string or an array of strings
			switch vv := elem.(type) {
			case string:
				if vv == "" {
					continue
				}
				inputs = append(inputs, []string{vv})
			case []any:
				var group []string
				for _, v := range vv {
					s, ok := v.(string)
					if !ok {
						return fmt.Errorf("igogpt: bulk input file must contain strings or arrays of strings")
					}
					if s == "" {
						continue
					}
					group = append(group, s)
				}
				if len(group) == 0 {
					continue
				}
				inputs = append(inputs, group)
			default:
				return fmt.Errorf("igogpt: bulk input file must contain strings or arrays of strings")
			}
		}
	} else {
		// Split by double newlines
		list := strings.Split(string(b), "\n\n")
		for _, elem := range list {
			// Split group by newlines
			group := strings.Split(elem, "\n")
			if len(group) == 0 {
				continue
			}
			// Remove empty lines
			var filtered []string
			for _, s := range group {
				if s != "" {
					filtered = append(filtered, s)
				}
			}
			inputs = append(inputs, filtered)
		}
	}

	// Create main chat
	var chatFunc func() (io.ReadWriter, func(), error)
	switch cfg.AI {
	case "bing":
		// Create bing client
		bingClient, err := bing.New(cfg.BingWait, &cfg.BingSession, cfg.BingSessionFile, cfg.Proxy)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create bing client: %w", err)
		}
		chatFunc = func() (io.ReadWriter, func(), error) {
			c, err := bingClient.Chat(ctx)
			if err != nil {
				return nil, nil, err
			}
			return c, func() { _ = c.Close }, nil
		}
	case "chatgpt":
		// Create chatgpt client
		client, err := chatgpt.New(ctx, cfg.ChatgptWait, cfg.ChatgptRemote, cfg.Proxy, true)
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create chatgpt client: %w", err)
		}
		defer client.Close()
		chatFunc = func() (io.ReadWriter, func(), error) {
			c, err := client.Chat(ctx, cfg.Model)
			if err != nil {
				return nil, nil, err
			}
			return c, func() { _ = c.Close }, nil
		}
	case "openai":
		// Create openai client
		client := openai.New(cfg.OpenaiKey, cfg.OpenaiWait, cfg.OpenaiMaxTokens)
		chatFunc = func() (io.ReadWriter, func(), error) {
			return client.Chat(ctx, cfg.Model, "system", fixed.NewFixedMemory(1, cfg.OpenaiMaxTokens)), func() {}, nil
		}
	default:
		return fmt.Errorf("igogpt: invalid ai: %s", cfg.AI)
	}

	var exit bool
	var output BulkOutput
	for _, prompts := range inputs {
		if exit {
			break
		}
		chat, close, err := chatFunc()
		if err != nil {
			return fmt.Errorf("igogpt: couldn't create chat: %w", err)
		}
		var msgs []inOut
		for _, prmpt := range prompts {
			if exit {
				break
			}
			// Check context
			select {
			case <-ctx.Done():
				exit = true
				continue
			default:
			}

			// Write to chat
			log.Println(prmpt)
			if _, err := chat.Write([]byte(prmpt)); err != nil {
				return fmt.Errorf("igogpt: couldn't write message to chatgpt: %w", err)
			}

			// Read from chat
			buf := make([]byte, 1024*64)
			n, err := chat.Read(buf)
			if err != nil {
				return fmt.Errorf("igogpt: couldn't read message from chatgpt: %w", err)
			}
			recv := string(buf[:n])
			log.Println(recv)

			msgs = append(msgs, inOut{In: prmpt, Out: recv})
		}
		close()
		output = append(output, msgs)
	}
	// Marshal output
	b, err = json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("igogpt: couldn't marshal output: %w", err)
	}
	// Create output directory if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(cfg.BulkOutput), 0755); err != nil {
		return fmt.Errorf("igogpt: couldn't create output directory: %w", err)
	}
	// Write output to file
	if err := os.WriteFile(cfg.BulkOutput, b, 0644); err != nil {
		return fmt.Errorf("igogpt: couldn't write output: %w", err)
	}
	return nil
}

// Cmd runs a command and returns the result
func Cmd(ctx context.Context, cfg *Config) error {
	// Bing chat not being available in this mode
	runner := command.New(&command.Config{
		Exit:      func() {},
		Output:    cfg.Output,
		Bing:      &notAvailable{},
		GoogleKey: cfg.GoogleKey,
		GoogleCX:  cfg.GoogleCX,
	})

	// TODO: custom parameter for json input
	result := runner.Run(ctx, cfg.Prompt)
	out, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}

type notAvailable struct{}

func (r *notAvailable) Read(p []byte) (n int, err error) {
	dummy := []byte("sorry, not available")
	copy(p, dummy)
	return len(dummy), nil
}

func (r *notAvailable) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func newLogger(rw io.ReadWriter, dir string) (io.ReadWriteCloser, error) {
	var f *os.File
	if dir != "" {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		filename := fmt.Sprintf("log_%s.txt", time.Now().Format("20060102_150405"))
		filename = filepath.Join(dir, filename)
		var err error
		f, err = os.OpenFile(filename, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
		if err != nil {
			return nil, err
		}
	}
	return &logger{
		ReadWriter: rw,
		file:       f,
	}, nil
}

type logger struct {
	io.ReadWriter
	file *os.File
}

func (l *logger) Close() error {
	if l.file == nil {
		return nil
	}
	return l.file.Close()
}

func (l *logger) Read(b []byte) (int, error) {
	n, err := l.ReadWriter.Read(b)
	if err != nil {
		return n, err
	}
	log.Println(">>>>>>>>>>>>>>>>>>>>")
	fmt.Println(string(b[:n]))
	if l.file != nil {
		fmt.Fprintf(l.file, "%s: >>>>>>>>>>>>>>>>>>>>\n", time.Now().Format("2006-01-02 15-04-05"))
		fmt.Fprintln(l.file, string(b[:n]))
	}
	return n, err
}

func (l *logger) Write(b []byte) (int, error) {
	n, err := l.ReadWriter.Write(b)
	if err != nil {
		return n, err
	}
	log.Println("<<<<<<<<<<<<<<<<<<<")
	fmt.Println(string(b[:n]))
	if l.file != nil {
		fmt.Fprintf(l.file, "%s: <<<<<<<<<<<<<<<<<<<\n", time.Now().Format("2006-01-02 15-04-05"))
		fmt.Fprintln(l.file, string(b[:n]))
	}
	return n, err
}

type exitChecker struct {
	io.ReadWriter
	exit string
}

func (e *exitChecker) Read(p []byte) (n int, err error) {
	n, err = e.ReadWriter.Read(p)
	if err != nil {
		return n, err
	}
	if strings.Contains(strings.ToLower(string(p[:n])), e.exit) {
		return n, fmt.Errorf("igogpt: exit condition detected")
	}
	return n, err
}
