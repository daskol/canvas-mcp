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

const defaultBaseURL = "https://lms.skoltech.ru"

var (
	baseURL   string
	tokenPath string
)

func init() {
	flag.StringVar(&baseURL, "base-url", envDefault("CANVAS_BASE_URL", defaultBaseURL), "Canvas HTTPS origin (required unless CANVAS_BASE_URL is set)")
	flag.StringVar(&tokenPath, "token-path", envDefault("CANVAS_TOKEN_FILE", "token"), "path to a Canvas access token file")
}

func main() {
	flag.Parse()

	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "canvas-mcp:", err)
		os.Exit(1)
	}
}

func run() error {
	if strings.TrimSpace(baseURL) == "" {
		return errors.New("--base-url or CANVAS_BASE_URL is required")
	}
	token, err := canvas.ReadToken(tokenPath)
	if err != nil {
		return err
	}
	client, err := canvas.New(baseURL, token)
	if err != nil {
		return err
	}

	ctx := context.Background()
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	err = mcpserver.New(client).Run(ctx, &mcp.StdioTransport{})
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func envDefault(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
