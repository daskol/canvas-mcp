// Package canvas implements the read-only portion of the Canvas REST API.
package canvas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/daskol/canvas-mcp/internal/buildinfo"
)

const maxResponseBytes = 8 << 20

type Client struct {
	base  *url.URL
	token string
	http  *http.Client
	gate  chan struct{}
}

// Result preserves Canvas fields and adds plain text beside rich HTML fields.
// NextPage is opaque: pass it back with the same tool to continue a list.
type Result struct {
	Data     any    `json:"data" jsonschema:"Canvas response with additional plain text fields for HTML content"`
	NextPage string `json:"next_page,omitempty" jsonschema:"Opaque URL for the next page; pass as page_url to the same tool"`
}

func ReadToken(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read token file: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsFunc(token, unicode.IsSpace) || strings.ContainsFunc(token, unicode.IsControl) {
		return "", errors.New("token file must contain one non-empty access token")
	}
	return token, nil
}

func New(baseURL, token string) (*Client, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("base URL must be an HTTPS origin, without a path, credentials, query, or fragment")
	}
	ip := net.ParseIP(base.Hostname())
	loopback := base.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())
	if base.Scheme != "https" && !(base.Scheme == "http" && loopback) {
		return nil, errors.New("base URL requires HTTPS (HTTP is allowed only on loopback for testing)")
	}
	if token == "" || strings.ContainsFunc(token, unicode.IsSpace) || strings.ContainsFunc(token, unicode.IsControl) {
		return nil, errors.New("access token is empty or contains whitespace/control characters")
	}
	return &Client{
		base: base, token: token, gate: make(chan struct{}, 1),
		http: &http.Client{
			Timeout: 30 * time.Second,
			// API reads should return JSON directly. Never forward credentials
			// to a login redirect, file host, or other endpoint.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// Get fetches one API response. Page URLs are limited to this origin and the
// requested endpoint; arbitrary URLs cannot receive the bearer token.
func (c *Client) Get(ctx context.Context, path string, query url.Values, pageURL string) (Result, error) {
	if !strings.HasPrefix(path, "/api/v1/") || strings.ContainsAny(path, "?#") {
		return Result{}, errors.New("invalid Canvas API path")
	}
	u := *c.base
	u.Path, u.RawPath, u.RawQuery = path, "", query.Encode()
	if pageURL != "" {
		next, err := c.pageURL(pageURL, path)
		if err != nil {
			return Result{}, err
		}
		u = *next
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Canvas charges an additional penalty for concurrent requests.
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return Result{}, errors.New("could not construct Canvas request")
		}
		request.Header.Set("Authorization", "Bearer "+c.token)
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "canvas-mcp/"+buildinfo.Version)
		response, err := c.http.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return Result{}, ctx.Err()
			}
			// Do not expose request URLs or proxy credentials from transport errors.
			return Result{}, errors.New("Canvas request failed; check network connectivity and the base URL")
		}
		if response.StatusCode == http.StatusTooManyRequests && attempt < 2 {
			delay := retryDelay(response.Header.Get("Retry-After"), attempt)
			response.Body.Close()
			if delay > 5*time.Second {
				return Result{}, fmt.Errorf("Canvas rate limit exceeded (HTTP 429); retry after %s", delay.Round(time.Second))
			}
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return Result{}, ctx.Err()
			}
			continue
		}
		result, err := c.decode(response, path)
		response.Body.Close()
		return result, err
	}
}

func (c *Client) decode(response *http.Response, path string) (Result, error) {
	if response.StatusCode != http.StatusOK {
		switch response.StatusCode {
		case http.StatusUnauthorized:
			return Result{}, errors.New("Canvas authentication failed (HTTP 401); check the token file and token expiry")
		case http.StatusForbidden:
			return Result{}, errors.New("Canvas denied access (HTTP 403); this token or account cannot access the requested content")
		case http.StatusNotFound:
			return Result{}, errors.New("Canvas content was not found or is not visible to this account (HTTP 404)")
		case http.StatusTooManyRequests:
			return Result{}, errors.New("Canvas rate limit exceeded (HTTP 429); retry later")
		default:
			return Result{}, fmt.Errorf("Canvas returned HTTP %d", response.StatusCode)
		}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return Result{}, errors.New("could not read Canvas response")
	}
	if len(data) > maxResponseBytes {
		return Result{}, errors.New("Canvas response exceeds 8 MiB; request fewer items with per_page")
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return Result{}, errors.New("Canvas returned invalid JSON; check the base URL and authentication")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Result{}, errors.New("Canvas returned trailing data after JSON")
	}
	result := Result{Data: value}
	addPlainText(value)
	if next := nextLink(response.Header.Values("Link")); next != "" {
		u, err := c.pageURL(next, path)
		if err != nil {
			return Result{}, errors.New("Canvas returned an unsafe pagination URL")
		}
		result.NextPage = u.String()
	}
	return result, nil
}

func (c *Client) pageURL(raw, path string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != c.base.Scheme || !strings.EqualFold(u.Host, c.base.Host) || u.User != nil || u.Fragment != "" || strings.TrimSuffix(u.Path, ".json") != strings.TrimSuffix(path, ".json") {
		return nil, errors.New("page_url must be a next_page URL from the same Canvas endpoint")
	}
	return u, nil
}

// Canvas Link headers use angle-bracketed absolute URLs. Match complete links
// so commas in a URL do not split it; rel can contain multiple relation names.
var (
	linkPattern = regexp.MustCompile(`<([^>]*)>\s*((?:;\s*[^,]*)*)`)
	relPattern  = regexp.MustCompile(`(?:^|;)\s*rel\s*=\s*(?:"([^"]*)"|([^;\s]+))`)
)

func nextLink(headers []string) string {
	for _, header := range headers {
		for _, match := range linkPattern.FindAllStringSubmatch(header, -1) {
			rel := relPattern.FindStringSubmatch(match[2])
			if len(rel) == 0 {
				continue
			}
			for _, name := range strings.Fields(rel[1] + " " + rel[2]) {
				if name == "next" {
					return match[1]
				}
			}
		}
	}
	return ""
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		// Bound conversion before multiplying to avoid overflowing Duration.
		if seconds > 3600 {
			seconds = 3600
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		return max(0, time.Until(date))
	}
	return time.Duration(1<<attempt) * time.Second
}
