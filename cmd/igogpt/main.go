package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"time"

	"github.com/igolaizola/igogpt"
	"github.com/igolaizola/igogpt/internal/session"
	"github.com/peterbourgon/ff/v3"
	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/peterbourgon/ff/v3/ffyaml"
)

// Build flags
var Version = ""
var Commit = ""
var Date = ""

func main() {
	// Create signal based context
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Launch command
	cmd := newCommand()
	if err := cmd.ParseAndRun(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func newCommand() *ffcli.Command {
	fs := flag.NewFlagSet("igogpt", flag.ExitOnError)

	return &ffcli.Command{
		ShortUsage: "igogpt [flags] <subcommand>",
		FlagSet:    fs,
		Exec: func(ctx context.Context, args []string) error {
			return flag.ErrHelp
		},
		Subcommands: []*ffcli.Command{
			newRunCommand("chat"),
			newRunCommand("auto"),
			newRunCommand("pair"),
			newRunCommand("cmd"),
			newCreateBingSessionCommand(),
			newVersionCommand(),
		},
	}
}

func newRunCommand(action string) *ffcli.Command {
	fs := flag.NewFlagSet(action, flag.ExitOnError)
	_ = fs.String("config", "igogpt.yaml", "config file (optional)")

	cfg := &igogpt.Config{}
	fs.StringVar(&cfg.AI, "ai", "chatgpt", "ai (openai, chatgpt, bing)")
	fs.StringVar(&cfg.Goal, "goal", "", "goal to achieve (ignored if prompt is provided)")
	fs.StringVar(&cfg.Prompt, "prompt", "", "the prompt to use instead of the default one (optional)")
	fs.StringVar(&cfg.Model, "model", "", "model (gpt-3.5-turbo, gpt-4)")
	fs.StringVar(&cfg.Proxy, "proxy", "", "proxy address (optional)")
	fs.StringVar(&cfg.Output, "output", "output", "output directory (optional)")
	fs.StringVar(&cfg.LogDir, "log", "logs", "log path, if empty, only logs to stdout (optional)")
	fs.IntVar(&cfg.Steps, "steps", 0, "number of steps to run, if unset, it will run until it exits (optional)")

	// Google
	fs.StringVar(&cfg.GoogleKey, "google-key", "", "google api key, see https://developers.google.com/custom-search/v1/introduction")
	fs.StringVar(&cfg.GoogleCX, "google-cx", "", "google cx (search engine ID), see https://cse.google.com/cse/all")

	// OpenAI
	fs.DurationVar(&cfg.OpenaiWait, "openai-wait", 5*time.Second, "wait between openai requests (optional)")
	fs.StringVar(&cfg.OpenaiKey, "openai-key", "", "openai key (optional)")
	fs.IntVar(&cfg.OpenaiMaxTokens, "openai-max-tokens", 5000, "openai max tokens per request")

	// Chatgpt
	fs.DurationVar(&cfg.ChatgptWait, "chatgpt-wait", 5*time.Second, "wait between chatgpt requests (optional)")
	fs.StringVar(&cfg.ChatgptRemote, "chatgpt-remote", "", "chatgpt browser remote debug address in the format `http://ip:port` (optional)")

	// Bing
	fs.DurationVar(&cfg.BingWait, "bing-wait", 5*time.Second, "wait between bing requests (optional)")
	fs.StringVar(&cfg.BingSessionFile, "bing-session", "bing-session.yaml", "bing session config file (optional)")
	fsBingSession := flag.NewFlagSet("bing", flag.ExitOnError)
	for _, fs := range []*flag.FlagSet{fs, fsBingSession} {
		pre := ""
		if fs.Name() != "bing" {
			pre = "bing-"
		}
		fs.StringVar(&cfg.BingSession.UserAgent, pre+"user-agent", "", "bing user agent")
		fs.StringVar(&cfg.BingSession.JA3, pre+"ja3", "", "bing ja3 fingerprint")
		fs.StringVar(&cfg.BingSession.Language, pre+"language", "", "bing language")
		fs.StringVar(&cfg.BingSession.Cookie, pre+"cookie", "", "bing cookie")
		fs.StringVar(&cfg.BingSession.SecMsGec, pre+"sec-ms-gec", "", "bing sec-ms-gec")
		fs.StringVar(&cfg.BingSession.SecMsGecVersion, pre+"sec-ms-gec-version", "", "bing sec-ms-gec-version")
		fs.StringVar(&cfg.BingSession.XClientData, pre+"x-client-data", "", "bing x-client-data")
		fs.StringVar(&cfg.BingSession.XMsUserAgent, pre+"x-ms-user-agent", "", "bing x-ms-user-agent")
	}

	return &ffcli.Command{
		Name:       action,
		ShortUsage: fmt.Sprintf("igogpt %s [flags] <key> <value data...>", action),
		Options: []ff.Option{
			ff.WithConfigFileFlag("config"),
			ff.WithConfigFileParser(ffyaml.Parser),
			ff.WithEnvVarPrefix("IGOGPT"),
		},
		ShortHelp: fmt.Sprintf("igogpt %s", action),
		FlagSet:   fs,
		Exec: func(ctx context.Context, args []string) error {
			loadSession(fsBingSession, cfg.BingSessionFile)
			return igogpt.Run(ctx, action, cfg)
		},
	}
}

func newCreateBingSessionCommand() *ffcli.Command {
	fs := flag.NewFlagSet("create-bing-session", flag.ExitOnError)
	_ = fs.String("config", "", "config file (optional)")

	cfg := &session.Config{}
	fs.StringVar(&cfg.Output, "output", "", "output yaml file (optional)")
	fs.StringVar(&cfg.Proxy, "proxy", "", "proxy server (optional)")
	fs.BoolVar(&cfg.Profile, "profile", false, "use profile (optional)")
	fs.StringVar(&cfg.Browser, "browser", "edge", "browser binary path or \"edge\" (optional)")
	fs.StringVar(&cfg.Remote, "remote", "", "remote debug address in the format `http://ip:port` (optional)")

	return &ffcli.Command{
		Name:       "create-bing-session",
		ShortUsage: "igogpt create-bing-session [flags] <key> <value data...>",
		Options: []ff.Option{
			ff.WithConfigFileFlag("config"),
			ff.WithConfigFileParser(ff.PlainParser),
			ff.WithEnvVarPrefix("IGOGPT"),
		},
		ShortHelp: "create bing session using browser",
		FlagSet:   fs,
		Exec: func(ctx context.Context, args []string) error {
			return session.Bing(ctx, cfg)
		},
	}
}

func newVersionCommand() *ffcli.Command {
	return &ffcli.Command{
		Name:       "version",
		ShortUsage: "igogpt version",
		ShortHelp:  "print version",
		Exec: func(ctx context.Context, args []string) error {
			v := Version
			if v == "" {
				if buildInfo, ok := debug.ReadBuildInfo(); ok {
					v = buildInfo.Main.Version
				}
			}
			if v == "" {
				v = "dev"
			}
			versionFields := []string{v}
			if Commit != "" {
				versionFields = append(versionFields, Commit)
			}
			if Date != "" {
				versionFields = append(versionFields, Date)
			}
			fmt.Println(strings.Join(versionFields, " "))
			return nil
		},
	}
}

func loadSession(fs *flag.FlagSet, file string) error {
	if file == "" {
		return fmt.Errorf("session file not specified")
	}
	if _, err := os.Stat(file); err != nil {
		return nil
	}
	log.Printf("loading session from %s", file)
	return ff.Parse(fs, []string{}, []ff.Option{
		ff.WithConfigFile(file),
		ff.WithConfigFileParser(ffyaml.Parser),
	}...)
}

type fsStrings []string

func (f *fsStrings) String() string {
	return strings.Join(*f, ",")
}

func (f *fsStrings) Set(value string) error {
	*f = append(*f, value)
	return nil
}
