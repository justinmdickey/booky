// Package version exposes Booky's build version, stamped at build time via
// -ldflags "-X github.com/justindickey/booky/internal/version.Version=v1.2.3".
package version

// Version is overridden at build time. "dev" when built without ldflags.
var Version = "dev"
