package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func doJSON(req *http.Request, httpClient *http.Client, out any) error {
	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.String(), err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read %s %s response: %w", req.Method, req.URL.String(), err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s returned %s: %s", req.Method, req.URL.String(), resp.Status, strings.TrimSpace(string(body)))
	}
	if len(body) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", req.Method, req.URL.String(), err)
	}

	return nil
}

func buildURL(base string, path string, query url.Values) string {
	trimmedBase := strings.TrimRight(base, "/")
	trimmedPath := strings.TrimLeft(path, "/")
	fullURL := trimmedBase + "/" + trimmedPath
	if len(query) == 0 {
		return fullURL
	}
	return fullURL + "?" + query.Encode()
}
