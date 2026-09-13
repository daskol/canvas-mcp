package mcpserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/daskol/canvas-mcp/internal/canvas"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	canvasClient, err := canvas.New(httpServer.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	server := New(canvasClient)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestToolsOverMCP(t *testing.T) {
	requests := make(chan *http.Request, 20)
	session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":42,"body":"<p>Course material &amp; notes.</p>"}`)
	}))
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 13 {
		t.Fatalf("got %d tools, want 13", len(tools.Tools))
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s lacks a read-only annotation", tool.Name)
		}
	}
	for _, test := range []struct {
		name, path string
		args       map[string]any
		query      url.Values
	}{
		{"get_profile", "/users/self/profile", nil, nil},
		{"list_courses", "/courses", map[string]any{"per_page": 2}, url.Values{"per_page": {"2"}, "include[]": {"term"}}},
		{"get_course", "/courses/42", map[string]any{"course_id": 42}, url.Values{"include[]": {"syllabus_body", "term", "total_scores"}}},
		{"list_assignments", "/courses/42/assignments", map[string]any{"course_id": 42, "search": "essay", "bucket": "upcoming"}, url.Values{"search_term": {"essay"}, "bucket": {"upcoming"}, "include[]": {"submission"}}},
		{"get_assignment", "/courses/42/assignments/7", map[string]any{"course_id": 42, "assignment_id": 7}, nil},
		{"list_modules", "/courses/42/modules", map[string]any{"course_id": 42}, nil},
		{"list_module_items", "/courses/42/modules/8/items", map[string]any{"course_id": 42, "module_id": 8}, url.Values{"include[]": {"content_details"}}},
		{"list_pages", "/courses/42/pages", map[string]any{"course_id": 42}, nil},
		{"get_page", "/courses/42/pages/lecture-notes", map[string]any{"course_id": 42, "page": "lecture-notes"}, nil},
		{"list_files", "/courses/42/files", map[string]any{"course_id": 42, "search": "slides"}, url.Values{"search_term": {"slides"}}},
		{"get_file", "/files/9", map[string]any{"file_id": 9}, nil},
		{"list_announcements", "/announcements", map[string]any{"course_id": 42}, url.Values{"context_codes[]": {"course_42"}, "start_date": {"1970-01-01"}, "active_only": {"true"}}},
		{"get_upcoming_work", "/planner/items", map[string]any{"start_date": "2026-09-12", "end_date": "2026-09-20"}, url.Values{"start_date": {"2026-09-12T00:00:00Z"}, "end_date": {"2026-09-20T00:00:00Z"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: test.name, Arguments: test.args})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("tool failed: %+v", result.Content)
			}
			var observed *http.Request
			select {
			case observed = <-requests:
			default:
				t.Fatal("tool did not call Canvas")
			}
			if observed.Method != "GET" || observed.URL.Path != "/api/v1"+test.path || observed.Header.Get("Authorization") != "Bearer test-token" {
				t.Fatalf("unexpected request method or path: %s %s", observed.Method, observed.URL.Path)
			}
			for key, want := range test.query {
				got := observed.URL.Query()[key]
				if strings.Join(got, "|") != strings.Join(want, "|") {
					t.Errorf("query %s: got %v, want %v", key, got, want)
				}
			}
			if test.name == "list_announcements" {
				end, err := time.Parse(time.RFC3339, observed.URL.Query().Get("end_date"))
				if err != nil || time.Since(end) > time.Minute {
					t.Fatal("announcement range does not extend to the present")
				}
			}
			// Round-trip through the SDK's actual output-schema validation.
			data, err := json.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var decoded struct {
				Data struct {
					BodyText string `json:"body_text"`
				} `json:"data"`
			}
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Data.BodyText != "Course material & notes." {
				t.Fatalf("missing readable content in structured result: %s", data)
			}
		})
	}
}

func TestToolErrorsAndInvalidInputs(t *testing.T) {
	requests := make(chan struct{}, 10)
	session := connect(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- struct{}{}
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"debug":"test-token"}`)
	}))
	for _, test := range []struct {
		name string
		args map[string]any
	}{
		{"get_course", nil},
		{"get_course", map[string]any{"course_id": -1}},
		{"get_course", map[string]any{"course_id": "42"}},
		{"get_page", map[string]any{"course_id": 42, "page": "../files"}},
		{"list_courses", map[string]any{"per_page": 101}},
		{"list_courses", map[string]any{"page_url": "https://example.com/api/v1/courses"}},
		{"get_upcoming_work", map[string]any{"start_date": "tomorrow"}},
		{"get_upcoming_work", map[string]any{"start_date": "2026-09-20", "end_date": "2026-09-12"}},
	} {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: test.name, Arguments: test.args})
		if err == nil && !result.IsError {
			t.Errorf("%s accepted invalid arguments", test.name)
		}
	}
	if len(requests) != 0 {
		t.Fatal("invalid input reached Canvas")
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "get_profile"})
	if err != nil || !result.IsError {
		t.Fatalf("authentication failure was not returned as a tool error: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "test-token") || !strings.Contains(string(encoded), "HTTP 401") {
		t.Fatal("tool error disclosed a credential")
	}
}
