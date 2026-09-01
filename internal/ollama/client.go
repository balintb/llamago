// Package ollama is a small, streaming aware client for Ollama HTTP API
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const DefaultHost = "http://127.0.0.1:11434"

// Client talks to a single Ollama server.
type Client struct {
	host string
	http *http.Client
}

// New creates a client, falling back to OLLAMA_HOST or DefaultHost
// Streaming uses per-request context cancellation; no global timeout
func New(host string) *Client {
	return &Client{
		host: ResolveHost(host),
		http: &http.Client{},
	}
}

// Host returns the client's normalized base URL.
func (c *Client) Host() string { return c.host }

// ResolveHost normalizes host string to full URL with scheme and default port
func ResolveHost(host string) string {
	if host == "" {
		host = os.Getenv("OLLAMA_HOST")
	}
	if host == "" {
		return DefaultHost
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil {
		return DefaultHost
	}
	if u.Port() == "" {
		u.Host += ":11434"
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u.String()
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.host+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("%s: %s", path, serverError(resp.StatusCode, msg))
	}
	return resp, nil
}

// serverError prefers JSON error message over status line
func serverError(code int, body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	if s := strings.TrimSpace(string(body)); s != "" {
		return fmt.Sprintf("%d: %s", code, s)
	}
	return http.StatusText(code)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// Version returns the server version, acting as health check with short timeout
func (c *Client) Version(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var v struct {
		Version string `json:"version"`
	}
	if err := c.getJSON(ctx, "/api/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}

// List returns every model available locally
func (c *Client) List(ctx context.Context) ([]Model, error) {
	var out struct {
		Models []Model `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/tags", &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

// PS returns models currently loaded in mem
func (c *Client) PS(ctx context.Context) ([]RunningModel, error) {
	var out struct {
		Models []RunningModel `json:"models"`
	}
	if err := c.getJSON(ctx, "/api/ps", &out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

// Show returns manifest, template, capabilities for a model
func (c *Client) Show(ctx context.Context, name string) (*ShowResponse, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/show", map[string]string{"model": name})
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var s ShowResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Delete removes model from local storage
func (c *Client) Delete(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/delete", map[string]string{"model": name})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// Unload evicts model from memory by requesting zero keep-alive
func (c *Client) Unload(ctx context.Context, name string) error {
	body := map[string]any{"model": name, "keep_alive": 0, "messages": []Message{}}
	resp, err := c.do(ctx, http.MethodPost, "/api/chat", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

// stream POSTs to path and invokes fn for each JSON chunk, stopping on error
func stream[T any](ctx context.Context, c *Client, path string, body any, fn func(T) error) error {
	resp, err := c.do(ctx, http.MethodPost, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	sc := bufio.NewScanner(resp.Body)
	// Expand scanner buffer beyond its 64KB default for larger chunks
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var chunk T
		if err := json.Unmarshal(line, &chunk); err != nil {
			return fmt.Errorf("decode %s chunk: %w", path, err)
		}
		if err := fn(chunk); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		// Report context cancellation instead of underlying read error
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// Chat streams a completion, calling fn per chunk - cancel ctx to stop early
func (c *Client) Chat(ctx context.Context, req ChatRequest, fn func(ChatResponse) error) error {
	req.Stream = true
	return stream(ctx, c, "/api/chat", req, fn)
}

// Pull downloads a model, calling fn for every progress chunk
func (c *Client) Pull(ctx context.Context, name string, fn func(PullProgress) error) error {
	body := map[string]any{"model": name, "stream": true}
	return stream(ctx, c, "/api/pull", body, fn)
}
