package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type client struct {
	addresses []string
	http      *http.Client
}

func newClient(addresses []string) *client {
	return &client{
		addresses: addresses,
		http:      &http.Client{Timeout: 15 * time.Second},
	}
}

// response is a successful HTTP exchange with an OpenBao node.
type response struct {
	status int
	body   []byte
}

// do sends the request to the first address that answers with a non-5xx
// status. Connection failures, 5xx responses and the sealed state move on to
// the next address; a 4xx response is final because it describes the request
// or the caller's credentials rather than the node.
func (c *client) do(method, path string, body []byte, token string) (response, error) {
	var lastDetail string
	for _, address := range c.addresses {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequest(method, address+path, reader)
		if err != nil {
			lastDetail = "malformed address"
			continue
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if token != "" {
			req.Header.Set("X-Vault-Token", token)
		}
		resp, err := c.http.Do(req)
		if err != nil {
			lastDetail = "connection failed"
			continue
		}
		payload, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastDetail = "response read failed"
			continue
		}
		if resp.StatusCode >= 500 {
			lastDetail = fmt.Sprintf("HTTP %d", resp.StatusCode)
			continue
		}
		return response{status: resp.StatusCode, body: payload}, nil
	}
	return response{}, fail(catAddress, "all %d address(es) unavailable (last: %s)", len(c.addresses), lastDetail)
}

// apiError is a parsed OpenBao error body. Only the status and the server's
// error strings are kept; request bodies are never echoed.
func apiErrors(body []byte) []string {
	var parsed struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	return parsed.Errors
}
