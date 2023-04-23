package command

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/igolaizola/igogpt/internal/google"
	"github.com/igolaizola/igogpt/internal/web"
)

type runner struct {
	bing     io.ReadWriter
	output   string
	commands map[string]Command
}

// New returns a new command runner.
func New(bing io.ReadWriter, googleKey, googleCX, output string, exit func()) *runner {
	cmds := []Command{
		&BashCommand{output: output},
		&BingCommand{chat: bing},
		&GoogleCommand{key: googleKey, cx: googleCX},
		&WebCommand{},
		NewNopCommand("talk"), NewNopCommand("think"),
		// File commands
		&ReadFileCommand{output: output},
		&WriteFileCommand{output: output},
		&DeleteFileCommand{output: output},
		&ListFilesCommand{output: output},
		&ExitCommand{exit: exit},
	}

	lookupCmds := map[string]Command{}
	for _, cmd := range cmds {
		lookupCmds[cmd.Name()] = cmd
	}
	return &runner{
		bing:     bing,
		output:   output,
		commands: lookupCmds,
	}
}

var ErrParse = fmt.Errorf("couldn't parse commands")

// Run runs commands based on an input string.
func (r *runner) Run(ctx context.Context, input string) []map[string]any {
	reqs, err := Parse(input)
	if err != nil {
		err := fmt.Errorf("couldn't parse commands: %w", err)
		log.Println(err)
		// Write input to file for debugging, use timestamp to avoid overwriting
		if err := os.WriteFile(fmt.Sprintf("error_%d.json", time.Now().Unix()), []byte(input), 0644); err != nil {
			log.Println(err)
		}
		results := []map[string]any{
			{"error": err.Error()},
		}
		return results
	}
	results := r.execute(ctx, reqs)
	return results
}

func (r *runner) execute(ctx context.Context, reqs []CommandRequest) []map[string]any {
	results := []map[string]any{}
	for _, req := range reqs {
		name := fixName(req.Name)
		cmd, ok := r.commands[name]
		if !ok {
			log.Println("command: unknown ", name)
			continue
		}
		cmdResult := cmd.Run(ctx, req.Args)
		if cmdResult == nil {
			continue
		}
		results = append(results, map[string]any{
			name: cmdResult,
		})
	}
	return results
}

func fixName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, " ", "_")
	switch name {
	case "write_file":
		return "write"
	case "read_file":
		return "read"
	case "delete_file":
		return "delete"
	case "list_files":
		return "list"
	}
	return name
}

// CommandRequest represents a single command request
type CommandRequest struct {
	// Command name
	Name string `json:"name"`
	// Command arguments
	Args []any `json:"args"`
}

// Parse tries to parse a JSON array of commands from the given text.
func Parse(text string) ([]CommandRequest, error) {
	// TODO: improve this to accept more invalid JSON inputs
	startIdx := strings.Index(text, "[")
	endIdx := strings.LastIndex(text, "]")
	if startIdx == -1 || endIdx == -1 {
		return nil, fmt.Errorf("no json array found: %s", text)
	}
	match := text[startIdx : endIdx+1]

	// Unmarshal the JSON array
	arr := []map[string]any{}
	if err := json.Unmarshal([]byte(match), &arr); err != nil {
		err := fmt.Errorf("couldn't unmarshal json array (%s): %w", match, err)
		// Try search for single commands
		pattern := `(\{.*?\})`
		re := regexp.MustCompile(pattern)
		matches := re.FindAllString(match, -1)
		if len(matches) == 0 {
			return nil, err
		}
		for _, m := range matches {
			obj := map[string]any{}
			if err := json.Unmarshal([]byte(m), &obj); err == nil {
				arr = append(arr, obj)
			}
		}
		if len(arr) == 0 {
			return nil, err
		}
	}

	// Convert the JSON array to a list of commands
	cmds := []CommandRequest{}
	for _, obj := range arr {
		for k, v := range obj {
			// Check if v is an array if not convert it to one
			varr, ok := v.([]any)
			if !ok {
				varr = []any{v}
			}

			cmds = append(cmds, CommandRequest{
				Name: k,
				Args: varr,
			})
		}
	}
	return cmds, nil
}

func logErr(err error) string {
	log.Println(err)
	return err.Error()
}

type Command interface {
	Name() string
	Run(ctx context.Context, args []any) any
}

// BashCommand executes a bash command
type BashCommand struct {
	output string
}

func (c *BashCommand) Name() string {
	return "bash"
}

func (c *BashCommand) Run(ctx context.Context, args []any) any {
	if len(args) == 0 {
		return logErr(fmt.Errorf("missing bash command"))
	}
	bashCmd := fmt.Sprintf("%s", args[0])
	if bashCmd == "" {
		return logErr(fmt.Errorf("empty bash command"))
	}
	// Execute bash command
	execCmd := exec.Command("bash", "-c", bashCmd)
	// Set the working directory for the command.
	execCmd.Dir = c.output
	bashOut, err := execCmd.CombinedOutput()
	if err != nil {
		log.Println(err, string(bashOut))
	}
	return string(bashOut)
}

// BingCommand asks bing chat the given question
type BingCommand struct {
	chat io.ReadWriter
}

func (c *BingCommand) Name() string {
	return "bing"
}

func (c *BingCommand) Run(ctx context.Context, args []any) any {
	if len(args) == 0 {
		return logErr(fmt.Errorf("missing bing message"))
	}
	query := fmt.Sprintf("%s", args[0])
	if query == "" {
		return logErr(fmt.Errorf("empty bing message"))
	}
	// Send message to bing
	if _, err := c.chat.Write([]byte(query)); err != nil {
		return logErr(fmt.Errorf("couldn't write message to bing: %w", err))
	}
	// Read message from bing
	bingBuf := make([]byte, 1024*64)
	bingN, err := c.chat.Read(bingBuf)
	if err != nil {
		return logErr(fmt.Errorf("couldn't read message from bing: %w", err))
	}
	bingRecv := string(bingBuf[:bingN])
	return bingRecv
}

type GoogleCommand struct {
	key string
	cx  string
}

func (c *GoogleCommand) Name() string {
	return "google"
}

func (c *GoogleCommand) Run(ctx context.Context, args []any) any {
	if len(args) == 0 {
		return logErr(fmt.Errorf("missing google query"))
	}
	query := fmt.Sprintf("%s", args[0])
	if query == "" {
		return logErr(fmt.Errorf("empty google query"))
	}
	results, err := google.Search(ctx, c.key, c.cx, query)
	if err != nil {
		return logErr(fmt.Errorf("couldn't search google: %w", err))
	}
	return results
}

type WebCommand struct{}

func (c *WebCommand) Name() string {
	return "web"
}

func (c *WebCommand) Run(ctx context.Context, args []any) any {
	if len(args) == 0 {
		return logErr(fmt.Errorf("missing web url"))
	}
	u := fmt.Sprintf("%s", args[0])
	if u == "" {
		return logErr(fmt.Errorf("empty web url"))
	}
	text, err := web.Text(ctx, u)
	if err != nil {
		return logErr(fmt.Errorf("couldn't obtain web: %w", err))
	}
	return text
}

// NewNopCommand creates a new nop command with the given name
func NewNopCommand(name string) *nopCommand {
	return &nopCommand{
		name: name,
	}
}

type nopCommand struct {
	name string
}

func (c *nopCommand) Name() string {
	return c.name
}

func (c *nopCommand) Run(ctx context.Context, args []any) any {
	return fmt.Sprintf("received %s command", c.Name())
}

// ReadFileCommand reads a file from the output directory
type ReadFileCommand struct {
	output string
}

func (c *ReadFileCommand) Name() string {
	return "read"
}

func (c *ReadFileCommand) Run(ctx context.Context, args []any) any {
	if len(args) == 0 {
		return logErr(fmt.Errorf("missing read file path"))
	}
	path := fmt.Sprintf("%s", args[0])
	if path == "" {
		return logErr(fmt.Errorf("empty read file path"))
	}

	// Read file
	path = filepath.Join(c.output, path)
	data, err := os.ReadFile(path)
	if err != nil {
		return logErr(fmt.Errorf("couldn't read file: %w", err))
	}
	return string(data)
}

// WriteFileCommand writes a file to the output directory
type WriteFileCommand struct {
	output string
}

func (c *WriteFileCommand) Name() string {
	return "write"
}

func (c *WriteFileCommand) Run(ctx context.Context, args []any) any {
	if len(args) < 1 {
		return logErr(fmt.Errorf("missing write file arguments"))
	}
	path := fmt.Sprintf("%s", args[0])
	if path == "" {
		return logErr(fmt.Errorf("empty write file path"))
	}
	content := ""
	if len(args) > 1 {
		content = fmt.Sprintf("%s", args[1])
	}
	// Create directory if it doesn't exist
	path = filepath.Join(c.output, path)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return logErr(fmt.Errorf("couldn't create directory: %w", err))
	}
	// Write file
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return logErr(fmt.Errorf("couldn't write file: %w", err))
	}
	return "write file success"
}

// DeleteFileCommand deletes a file from the output directory
type DeleteFileCommand struct {
	output string
}

func (c *DeleteFileCommand) Name() string {
	return "delete"
}

func (c *DeleteFileCommand) Run(ctx context.Context, args []any) any {
	if len(args) == 0 {
		return logErr(fmt.Errorf("missing delete file path"))
	}
	path := fmt.Sprintf("%s", args[0])
	if path == "" {
		return logErr(fmt.Errorf("empty delete file path"))
	}
	// Delete file
	path = filepath.Join(c.output, path)
	if err := os.Remove(path); err != nil {
		return logErr(fmt.Errorf("couldn't delete file: %w", err))
	}
	return "delete file success"
}

// ListFilesCommand lists files in the output directory
type ListFilesCommand struct {
	output string
}

func (c *ListFilesCommand) Name() string {
	return "list"
}

func (c *ListFilesCommand) Run(ctx context.Context, args []any) any {
	if len(args) == 0 {
		return logErr(fmt.Errorf("missing list_files path"))
	}
	path := fmt.Sprintf("%s", args[0])
	if path == "" {
		return logErr(fmt.Errorf("empty list_files path"))
	}
	// List files
	path = filepath.Join(c.output, path)
	var items []string
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		name := info.Name()
		if info.IsDir() {
			name += "/"
		}
		items = append(items, info.Name())
		return nil
	})
	if err != nil {
		return logErr(fmt.Errorf("couldn't list files: %w", err))
	}
	return items
}

type ExitCommand struct {
	exit func()
}

func (c *ExitCommand) Name() string {
	return "exit"
}

func (c *ExitCommand) Run(ctx context.Context, args []any) any {
	if c.exit != nil {
		c.exit()
	}
	return "exit"
}
