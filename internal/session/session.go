package session

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/go-rod/stealth"
	"github.com/igolaizola/igogpt/internal/scrapfly"
	"github.com/igolaizola/igogpt/pkg/bing"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Remote  string
	Browser string
	Proxy   string
	Profile bool
	Output  string
	Service string
}

func Bing(ctx context.Context, cfg *Config) error {
	output := cfg.Output
	if output == "" {
		output = "bing-session.yaml"
	}
	if fi, err := os.Stat(output); err == nil && fi.IsDir() {
		return fmt.Errorf("output file is a directory: %s", output)
	}

	log.Println("Starting browser")
	defer log.Println("Browser stopped")

	var cancel context.CancelFunc

	if cfg.Remote != "" {
		log.Println("session: connecting to browser at", cfg.Remote)
		log.Println("session: disconnecting from browser at", cfg.Remote)

		ctx, cancel = chromedp.NewRemoteAllocator(ctx, cfg.Remote)
		defer cancel()
	} else {
		log.Println("session: launching browser")
		defer log.Println("session: browser stopped")

		opts := append(
			chromedp.DefaultExecAllocatorOptions[3:],
			chromedp.NoFirstRun,
			chromedp.NoDefaultBrowserCheck,
			chromedp.Flag("headless", false),
		)

		if cfg.Proxy != "" {
			opts = append(opts,
				chromedp.ProxyServer(cfg.Proxy),
			)
		}

		if cfg.Profile {
			opts = append(opts,
				// if user-data-dir is set, chrome won't load the default profile,
				// even if it's set to the directory where the default profile is stored.
				// set it to empty to prevent chromedp from setting it to a temp directory.
				chromedp.UserDataDir(""),
				chromedp.Flag("disable-extensions", false),
			)
		}

		// Custom binary
		execPath := cfg.Browser
		if execPath != "" {
			// If binary is "edge", try to find the edge binary
			if execPath == "edge" {
				binaryCandidate, err := edgeBinary()
				if err != nil {
					return err
				}
				execPath = binaryCandidate
			}
			log.Println("using browser:", execPath)
			opts = append(opts,
				chromedp.ExecPath(execPath),
			)
		}

		ctx, cancel = chromedp.NewExecAllocator(ctx, opts...)
		defer cancel()
	}

	// create chrome instance
	ctx, cancel = chromedp.NewContext(
		ctx,
		// chromedp.WithDebugf(log.Printf),
	)
	defer cancel()

	// Launch stealth plugin
	if err := chromedp.Run(
		ctx,
		chromedp.Evaluate(stealth.JS, nil),
	); err != nil {
		return fmt.Errorf("session: could not launch stealth plugin: %w", err)
	}

	// disable webdriver
	if err := chromedp.Run(ctx, chromedp.ActionFunc(func(cxt context.Context) error {
		_, err := page.AddScriptToEvaluateOnNewDocument("Object.defineProperty(navigator, 'webdriver', { get: () => false, });").Do(cxt)
		if err != nil {
			return err
		}
		return nil
	})); err != nil {
		return fmt.Errorf("could not disable webdriver: %w", err)
	}

	// check if webdriver is disabled
	if err := chromedp.Run(ctx,
		chromedp.Navigate("https://intoli.com/blog/not-possible-to-block-chrome-headless/chrome-headless-test.html"),
	); err != nil {
		return fmt.Errorf("could not navigate to test page: %w", err)
	}
	<-time.After(1 * time.Second)

	// obtain ja3
	var ja3 string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(scrapfly.FPJA3URL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			node, err := dom.GetDocument().Do(ctx)
			if err != nil {
				return err
			}
			res, err := dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
			if err != nil {
				return err
			}
			doc, err := goquery.NewDocumentFromReader(bytes.NewBuffer([]byte(res)))
			if err != nil {
				return err
			}
			body := doc.Find("body").Text()
			if body == "" {
				return errors.New("couldn't obtain fp ja3")
			}
			var fpJA3 scrapfly.FPJA3
			if err := json.Unmarshal([]byte(body), &fpJA3); err != nil {
				return err
			}
			ja3 = fpJA3.JA3
			if ja3 == "" {
				return errors.New("empty ja3")
			}
			log.Println("ja3:", ja3)
			return nil
		}),
	); err != nil {
		return fmt.Errorf("could not obtain ja3: %w", err)
	}
	if ja3 == "" {
		return errors.New("empty ja3")
	}

	// obtain user agent
	var userAgent, acceptLanguage string
	if err := chromedp.Run(ctx,
		chromedp.Navigate(scrapfly.InfoHTTPURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.ActionFunc(func(ctx context.Context) error {
			node, err := dom.GetDocument().Do(ctx)
			if err != nil {
				return err
			}
			res, err := dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
			if err != nil {
				return err
			}
			doc, err := goquery.NewDocumentFromReader(bytes.NewBuffer([]byte(res)))
			if err != nil {
				return err
			}
			body := doc.Find("body").Text()
			if body == "" {
				return errors.New("couldn't obtain info http")
			}
			var infoHTTP scrapfly.InfoHTTP
			if err := json.Unmarshal([]byte(body), &infoHTTP); err != nil {
				return err
			}
			userAgent = infoHTTP.Headers.UserAgent.Payload
			if userAgent == "" {
				return errors.New("empty user agent")
			}
			log.Println("user-agent:", userAgent)
			v, ok := infoHTTP.Headers.ParsedHeaders["Accept-Language"]
			if !ok || len(v) == 0 {
				return errors.New("empty accept language")
			}
			acceptLanguage = v[0]
			log.Println("language:", acceptLanguage)
			return nil
		}),
	); err != nil {
		return fmt.Errorf("session: could not obtain user agent: %w", err)
	}
	if userAgent == "" {
		return errors.New("session: empty user agent")
	}
	if acceptLanguage == "" {
		return errors.New("session: empty accept language")
	}

	// Enable network events
	if err := chromedp.Run(ctx,
		network.Enable(),
	); err != nil {
		return fmt.Errorf("session: could not enable network events: %w", err)
	}

	var lck sync.Mutex

	wait, done := context.WithCancel(context.Background())
	defer done()

	// Obtain bing cookie, sec-ms-gec, x-client-data, x-ms-useragent
	s := &bing.Session{
		JA3:       ja3,
		UserAgent: userAgent,
		Language:  acceptLanguage,
	}

	chromedp.ListenTarget(
		ctx,
		func(ev interface{}) {
			e, ok := ev.(*network.EventRequestWillBeSentExtraInfo)
			if !ok {
				return
			}
			path := getHeader(e, ":path")
			if path != "/turing/conversation/create" {
				return
			}
			lck.Lock()
			defer lck.Unlock()
			if h := getHeader(e, "cookie"); h != "" {
				if s.Cookie != h {
					s.Cookie = h
					log.Println("cookie:", "...redacted...")
				}
			}

			if h := getHeader(e, "sec-ms-gec"); h != "" {
				if s.SecMsGec != h {
					s.SecMsGec = h
					log.Println("sec-ms-gec:", h)
				}
			}

			if h := getHeader(e, "sec-ms-gec-version"); h != "" {
				if s.SecMsGecVersion != h {
					s.SecMsGecVersion = h
					log.Println("sec-ms-gec-version:", h)
				}
			}

			if h := getHeader(e, "x-client-data"); h != "" {
				if s.XClientData != h {
					s.XClientData = h
					log.Println("x-client-data:", h)
				}
			}

			if h := getHeader(e, "x-ms-useragent"); h != "" {
				if s.XMsUserAgent != h {
					s.XMsUserAgent = h
					log.Println("x-ms-useragent:", h)
				}
			}

			if s.Cookie == "" {
				return
			}
			done()
		},
	)

	if err := chromedp.Run(ctx,
		// Load google first to have a sane referer
		chromedp.Navigate("https://www.google.com/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Navigate("https://www.bing.com/search?q=Bing+AI&showconv=1&FORM=hpcodx"),
		chromedp.WaitReady("body", chromedp.ByQuery),
		// Obtain body
		chromedp.ActionFunc(func(ctx context.Context) error {
			node, err := dom.GetDocument().Do(ctx)
			if err != nil {
				return err
			}
			res, err := dom.GetOuterHTML().WithNodeID(node.NodeID).Do(ctx)
			if err != nil {
				return err
			}
			doc, err := goquery.NewDocumentFromReader(bytes.NewBuffer([]byte(res)))
			if err != nil {
				return err
			}
			body := doc.Find("body")
			if body == nil {
				return errors.New("couldn't obtain bing body")
			}
			fmt.Println(body.Html())
			return nil
		}),
	); err != nil {
		return fmt.Errorf("session: couldn't obtain bing data: %w", err)
	}

	fmt.Println("Type anything and click send once the conversation is loaded")

	// Wait for session to be obtained
	select {
	case <-wait.Done():
	case <-ctx.Done():
		return ctx.Err()
	}

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("session: couldn't marshal session: %w", err)
	}
	log.Println("Session successfully obtained")

	// If the file already exists, copy it to a backup file
	if _, err := os.Stat(output); err == nil {
		backup := output
		ext := filepath.Ext(backup)
		// Remove the extension from the output
		backup = strings.TrimSuffix(backup, ext)
		// Add a timestamp to the backup file
		backup = fmt.Sprintf("%s_%s%s", backup, time.Now().Format("20060102150405"), ext)
		if err := os.Rename(output, backup); err != nil {
			return fmt.Errorf("couldn't backup session: %w", err)
		}
		log.Println("Previous session backed up to", backup)
	}

	// Write the session to the output file
	if err := os.WriteFile(output, data, 0644); err != nil {
		return fmt.Errorf("couldn't write session: %w", err)
	}
	log.Println("Session saved to", output)
	return nil
}

func getHeader(e *network.EventRequestWillBeSentExtraInfo, k string) string {
	v := e.Headers[k]
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func edgeBinary() (string, error) {
	switch runtime.GOOS {
	case "windows":
		// Check for x64
		if runtime.GOARCH == "amd64" {
			path := `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				return path, nil
			}
		}

		// Check for x86
		path := `C:\Program Files\Microsoft\Edge\Application\msedge.exe`
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return path, nil
		}

	case "darwin":
		path := "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge"
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return path, nil
		}

	case "linux":
		path := "/opt/microsoft/msedge/msedge"
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			return path, nil
		}
	}

	return "", errors.New("session: edge browser binary not found")
}
