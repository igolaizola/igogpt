package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Text returns the text of the given URL.
func Text(ctx context.Context, u string) (string, error) {
	// Add protocol if missing.
	if !strings.HasPrefix(u, "http") && !strings.HasPrefix(u, "https") {
		u = "https://" + u
	}

	// Create client and request.
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", fmt.Errorf("web: couldn't create request: %w", err)
	}
	req = req.WithContext(ctx)

	// Get response.
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("web: couldn't get response: %w", err)
	}
	defer resp.Body.Close()

	// Parse response.
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("web: couldn't parse response: %w", err)
	}
	text := doc.Text()

	// Condense the text.
	text = strings.TrimSpace(text)
	for _, c := range []string{"\t", "\n", "\r"} {
		text = strings.ReplaceAll(text, c, " ")
	}
	for i := 0; i < 10; i++ {
		text = strings.ReplaceAll(text, "  ", " ")
	}

	// Limit the text.
	if len(text) > 1000 {
		text = text[:1000]
	}
	return text, nil
}
