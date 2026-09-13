package canvas

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadToken(t *testing.T) {
	for _, test := range []struct {
		name, contents string
		valid          bool
	}{
		{"newline", "secret\n", true}, {"CRLF", "secret\r\n", true},
		{"empty", "\n", false}, {"multiple", "secret\nother", false},
		{"control", "secret\x00", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "token")
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			token, err := ReadToken(path)
			if (err == nil) != test.valid || (test.valid && token != "secret") {
				t.Fatalf("token acceptance mismatch; err=%v", err)
			}
		})
	}
}

func TestPaginationAndContent(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("expected an authenticated GET")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "bookmark,2" {
			if r.URL.Query().Get("include[]") != "term" {
				t.Error("pagination dropped the opaque query")
			}
			fmt.Fprint(w, `[{"id":2}]`)
			return
		}
		w.Header().Add("Link", fmt.Sprintf(`<%s/api/v1/courses?page=1>; rel="current"`, server.URL))
		w.Header().Add("Link", fmt.Sprintf(`<%s/api/v1/courses.json?page=bookmark,2&include%%5B%%5D=term>; rel="next"`, server.URL))
		fmt.Fprint(w, `[{"id":9007199254740993,"syllabus_body":"<p>Hello <b>world</b>.</p>"}]`)
	}))
	defer server.Close()
	client, err := New(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Get(t.Context(), "/api/v1/courses", url.Values{"per_page": {"1"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	item := first.Data.([]any)[0].(map[string]any)
	if item["id"].(json.Number).String() != "9007199254740993" || item["syllabus_body_text"] != "Hello world." {
		t.Fatalf("response lost numeric precision or text: %v", item)
	}
	if first.NextPage == "" {
		t.Fatal("pagination was omitted")
	}
	second, err := client.Get(t.Context(), "/api/v1/courses", nil, first.NextPage)
	if err != nil || second.NextPage != "" {
		t.Fatalf("next page: %v", err)
	}
}

func TestCredentialBoundaries(t *testing.T) {
	var foreignRequests atomic.Int32
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		foreignRequests.Add(1)
		fmt.Fprint(w, `{}`)
	}))
	defer foreign.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, foreign.URL+"/api/v1/courses", http.StatusFound)
	}))
	defer server.Close()
	client, _ := New(server.URL, "secret")
	for _, cursor := range []string{
		foreign.URL + "/api/v1/courses", server.URL + "/api/v1/users/self/profile",
		server.URL + "/api/v1/courses/../users", server.URL + "/api/v1/courses#fragment",
		strings.Replace(server.URL, "://", "://user:password@", 1) + "/api/v1/courses",
	} {
		if _, err := client.Get(t.Context(), "/api/v1/courses", nil, cursor); err == nil {
			t.Error("accepted an unsafe pagination URL")
		}
	}
	if _, err := client.Get(t.Context(), "/api/v1/courses", nil, ""); err == nil {
		t.Error("accepted an API redirect")
	}
	if foreignRequests.Load() != 0 {
		t.Fatal("a request escaped the configured Canvas origin")
	}
	if _, err := New("http://example.com", "secret"); err == nil {
		t.Fatal("accepted plaintext remote authentication")
	}
}

func TestFailuresDoNotExposeResponseBodies(t *testing.T) {
	for _, status := range []int{200, 401, 403, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				fmt.Fprint(w, "secret and private server diagnostics")
			}))
			defer server.Close()
			client, _ := New(server.URL, "secret")
			_, err := client.Get(t.Context(), "/api/v1/courses", nil, "")
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "private") {
				t.Fatalf("expected a sanitized error; got %v", err)
			}
		})
	}
}

func TestRateLimitRetryAndCancellation(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	client, _ := New(server.URL, "secret")
	if _, err := client.Get(t.Context(), "/api/v1/courses", nil, ""); err != nil {
		t.Fatal(err)
	}
	if attempts.Load() != 3 {
		t.Fatalf("attempts = %d", attempts.Load())
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := client.Get(ctx, "/api/v1/courses", nil, ""); err == nil {
		t.Fatal("ignored cancellation")
	}
	if retryDelay("999999999", 0) != time.Hour {
		t.Fatal("large Retry-After value was not bounded")
	}
}

func TestRejectUnsafeResponsePagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Link", `<https://example.com/api/v1/courses>; rel="next"`)
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	client, _ := New(server.URL, "secret")
	if _, err := client.Get(t.Context(), "/api/v1/courses", nil, ""); err == nil {
		t.Fatal("returned an unsafe next_page")
	}
}
