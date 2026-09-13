// Package buildinfo holds metadata supplied by the release build.
package buildinfo

// Version is set from the Git tag with go build -ldflags -X.
var Version = "dev"
