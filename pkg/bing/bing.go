package bing

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	gohttp "net/http"
	"net/url"
	"os"
	"time"

	http "github.com/Danny-Dasilva/fhttp"
	"github.com/dsnet/compress/brotli"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	inthttp "github.com/igolaizola/igogpt/internal/http"
	"github.com/igolaizola/igogpt/internal/ratelimit"
	"gopkg.in/yaml.v2"
)

type Session struct {
	JA3             string `yaml:"ja3"`
	UserAgent       string `yaml:"user-agent"`
	Language        string `yaml:"language"`
	Cookie          string `yaml:"cookie"`
	SecMsGec        string `yaml:"sec-ms-gec"`
	SecMsGecVersion string `yaml:"sec-ms-gec-version"`
	XClientData     string `yaml:"x-client-data"`
	XMsUserAgent    string `yaml:"x-ms-user-agent"`
}

type Client struct {
	client      *http.Client
	proxy       string
	session     *Session
	sessionFile string
	rateLimit   ratelimit.Lock
}

// New creates a new bing client
func New(wait time.Duration, session *Session, sessionFile string, proxy string) (*Client, error) {
	// Configure rate limit
	if wait == 0 {
		wait = 5 * time.Second
	}
	rateLimit := ratelimit.New(wait)

	// Create http client
	httpClient, err := inthttp.NewClient(session.JA3, session.UserAgent, session.Language, proxy)
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't create http client: %w", err)
	}
	httpClient.Timeout = 5 * time.Minute
	if err := inthttp.SetCookies(httpClient, "https://www.bing.com", session.Cookie); err != nil {
		return nil, fmt.Errorf("bing: couldn't set cookies: %w", err)
	}

	c := &Client{
		client:      httpClient,
		session:     session,
		sessionFile: sessionFile,
		proxy:       proxy,
		rateLimit:   rateLimit,
	}
	return c, nil
}

type Conversation struct {
	ConversationId        string              `json:"conversationId,omitempty"`
	ClientId              string              `json:"clientId,omitempty"`
	ConversationSignature string              `json:"conversationSignature,omitempty"`
	Result                *ConversationResult `json:"result"`
}

type ConversationResult struct {
	Value   *string `json:"value"`
	Message *string `json:"message"`
}

// Chat creates a new chat session.
func (c *Client) Chat(ctx context.Context) (io.ReadWriteCloser, error) {
	u := "https://www.bing.com/turing/conversation/create"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't create request: %w", err)
	}
	c.addHeaders(req)

	// Rate limit requests
	unlock := c.rateLimit.Lock(ctx)
	defer unlock()

	// Send conversation create request
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't do request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body using brotli reader
	brotliReader, err := brotli.NewReader(resp.Body, nil)
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't create brotli reader: %w", err)
	}
	defer brotliReader.Close()
	response, err := io.ReadAll(brotliReader)
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't read response: %w", err)
	}

	// Check conversation create response
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bing: invalid status code: %s (%s)", resp.Status, string(response))
	}
	var conversation Conversation
	if err := json.Unmarshal(response, &conversation); err != nil {
		return nil, fmt.Errorf("bing: couldn't unmarshal response (%s): %w", string(response), err)
	}
	if conversation.Result == nil || conversation.Result.Value == nil || *conversation.Result.Value != "Success" {
		return nil, fmt.Errorf("bing: invalid conversation result: %s", string(response))
	}

	// Update session with the new cookies
	cookie, err := inthttp.GetCookies(c.client, "https://www.bing.com")
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't get cookies: %w", err)
	}
	c.session.Cookie = cookie
	data, err := yaml.Marshal(c.session)
	if err != nil {
		return nil, fmt.Errorf("bing: couldn't marshal session: %w", err)
	}
	if err := os.WriteFile(c.sessionFile, data, 0644); err != nil {
		return nil, fmt.Errorf("bing: couldn't write session: %w", err)
	}

	// Create websocket dialer
	dialer := &websocket.Dialer{}
	if c.proxy != "" {
		u, err := url.Parse(c.proxy)
		if err != nil {
			return nil, fmt.Errorf("bing: couldn't parse proxy url: %w", err)
		}
		dialer.Proxy = gohttp.ProxyURL(u)
	}

	// Dial websocket
	ws, wsResp, err := dialer.Dial("wss://sydney.bing.com/sydney/ChatHub", c.wsHeaders())
	if err != nil {
		if wsResp != nil && wsResp.Body != nil {
			defer wsResp.Body.Close()
			body, _ := io.ReadAll(wsResp.Body)
			return nil, fmt.Errorf("bing: couldn't dial websocket (%s): %w", body, err)
		}
		return nil, fmt.Errorf("bing: couldn't dial websocket: %w", err)
	}

	// Send initial message
	message := []byte("{\"protocol\": \"json\", \"version\": 1}" + delimiter)
	if err := ws.WriteMessage(websocket.TextMessage, message); err != nil {
		return nil, fmt.Errorf("bing: couldn't send initial message: %w", err)
	}
	if _, _, err := ws.ReadMessage(); err != nil {
		return nil, fmt.Errorf("bing: couldn't read initial message: %w", err)
	}

	// Create connection
	ctx, cancel := context.WithCancel(ctx)
	rd, wr := io.Pipe()
	return &conn{
		ctx:          ctx,
		cancel:       cancel,
		pipeReader:   rd,
		pipeWriter:   wr,
		ws:           ws,
		conversation: &conversation,
		rateLimit:    c.rateLimit,
	}, nil
}

// Close closes the chat connection.
func (c *conn) Close() error {
	c.cancel()
	_ = c.pipeReader.Close()
	if err := c.ws.Close(); err != nil {
		return fmt.Errorf("bing: couldn't close websocket: %w", err)
	}
	return nil
}

func (c *Client) wsHeaders() gohttp.Header {
	cookie, err := inthttp.GetCookies(c.client, "https://www.bing.com")
	if err != nil {
		return nil
	}
	return gohttp.Header{
		"Pragma":                   []string{"no-cache"},
		"Cache-Control":            []string{"no-cache"},
		"Cookie":                   []string{cookie},
		"User-Agent":               []string{"c.session.UserAgent"},
		"Origin":                   []string{"https://www.bing.com"},
		"Accept-Encoding":          []string{"gzip, deflate, br"},
		"Accept-Language":          []string{c.session.Language},
		"Sec-WebSocket-Extensions": []string{"permessage-deflate; client_max_window_bits"},
	}
}

func (c *Client) addHeaders(req *http.Request) {
	// Add headers
	req.Header = http.Header{
		"accept":                      []string{"application/json"},
		"accept-encoding":             []string{"gzip, deflate, br, zsdch"},
		"accept-language":             []string{"es,es-ES;q=0.9,en;q=0.8,en-GB;q=0.7,en-US;q=0.6"},
		"referer":                     []string{"https://www.bing.com/search?q=Bing+AI&showconv=1&FORM=hpcodx"},
		"sec-ch-ua":                   []string{"\"Chromium\";v=\"112\", \"Microsoft Edge\";v=\"112\", \"Not:A-Brand\";v=\"99\""},
		"sec-ch-ua-arch":              []string{"x86"},
		"sec-ch-ua-bitness":           []string{"64"},
		"sec-ch-ua-full-version":      []string{"112.0.1722.39"},
		"sec-ch-ua-full-version-list": []string{"\"Chromium\";v=\"112.0.5615.49\", \"Microsoft Edge\";v=\"112.0.1722.39\", \"Not:A-Brand\";v=\"99.0.0.0\""},
		"sec-ch-ua-mobile":            []string{"?0"},
		"sec-ch-ua-model":             []string{""},
		"sec-ch-ua-platform":          []string{"Windows"},
		"sec-ch-ua-platform-version":  []string{"10.0.0"},
		"sec-fetch-dest":              []string{"empty"},
		"sec-fetch-mode":              []string{"cors"},
		"sec-fetch-site":              []string{"same-origin"},
		"sec-ms-gec":                  []string{c.session.SecMsGec},
		"sec-ms-gec-version":          []string{c.session.SecMsGecVersion},
		"x-client-data":               []string{c.session.XClientData},
		"x-ms-client-request-id":      []string{uuid.NewString()},
		"x-ms-useragent":              []string{c.session.XMsUserAgent},
	}

	// Add headers order
	req.Header[http.HeaderOrderKey] = []string{
		"accept",
		"accept-encoding",
		"accept-language",
		"cookie",
		"referer",
		"sec-ch-ua",
		"sec-ch-ua-arch",
		"sec-ch-ua-bitness",
		"sec-ch-ua-full-version",
		"sec-ch-ua-full-version-list",
		"sec-ch-ua-mobile",
		"sec-ch-ua-model",
		"sec-ch-ua-platform",
		"sec-ch-ua-platform-version",
		"sec-fetch-dest",
		"sec-fetch-mode",
		"sec-fetch-site",
		"sec-ms-gec",
		"sec-ms-gec-version",
		"user-agent",
		"x-client-data",
		"x-ms-client-request-id",
		"x-ms-useragent",
	}
	req.Header[http.PHeaderOrderKey] = []string{
		":authority",
		":method",
		":path",
		":scheme",
	}
}
