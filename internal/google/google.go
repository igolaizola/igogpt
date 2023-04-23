package google

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
)

const (
	apiURL = "https://www.googleapis.com/customsearch/v1"
	cx     = "YOUR_CX"
)

type SearchResponse struct {
	Items []SearchResult `json:"items"`
}

type SearchResult struct {
	Title string `json:"title"`
	Link  string `json:"link"`
}

func Search(ctx context.Context, key, cx, query string) ([]SearchResult, error) {
	reqURL := fmt.Sprintf("%s?key=%s&cx=%s&q=%s", apiURL, key, cx, url.QueryEscape(query))
	response, err := http.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("error making HTTP request: %w", err)
	}
	defer response.Body.Close()

	body, err := ioutil.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %w", err)
	}

	var searchResponse SearchResponse
	if err := json.Unmarshal(body, &searchResponse); err != nil {
		return nil, fmt.Errorf("error unmarshaling JSON response: %w", err)
	}

	return searchResponse.Items, nil
}
