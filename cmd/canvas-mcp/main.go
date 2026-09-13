package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/daskol/canvas-mcp/internal/canvas"
	"github.com/daskol/canvas-mcp/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	baseURL := flag.String("base-url", envDefault("CANVAS_BASE_URL", ""), "Canvas HTTPS origin (required unless CANVAS_BASE_URL is set)")
	tokenFile := flag.String("token-file", envDefault("CANVAS_TOKEN_FILE", "token"), "path to a Canvas access token file")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "canvas-mcp: unexpected positional arguments")
		os.Exit(2)
	}
	if strings.TrimSpace(*baseURL) == "" {
		fail(errors.New("--base-url or CANVAS_BASE_URL is required"))
	}
	token, err := canvas.ReadToken(*tokenFile)
	if err != nil {
		fail(err)
	}
	client, err := canvas.New(*baseURL, token)
	if err != nil {
		fail(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpserver.New(client).Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		fail(err)
	}
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "canvas-mcp:", err)
	os.Exit(1)
}
