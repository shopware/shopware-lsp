package uriutil

import (
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// Path converts an LSP file URI to an operating-system path. Plain paths are
// accepted for compatibility with older clients.
func Path(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("empty URI")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse URI %q: %w", value, err)
	}
	if parsed.Scheme == "" {
		return filepath.Clean(value), nil
	}
	if parsed.Scheme != "file" {
		return "", fmt.Errorf("unsupported URI scheme %q", parsed.Scheme)
	}

	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return "", fmt.Errorf("decode file URI %q: %w", value, err)
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		path = "//" + parsed.Host + path
	}
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.FromSlash(path), nil
}

// FileURI converts a path to a standards-compliant escaped file URI.
func FileURI(path string) string {
	cleaned := filepath.Clean(path)
	slashPath := filepath.ToSlash(cleaned)
	if runtime.GOOS == "windows" && len(slashPath) >= 2 && slashPath[1] == ':' {
		slashPath = "/" + slashPath
	}
	return (&url.URL{Scheme: "file", Path: slashPath}).String()
}

// FileURIWithFragment creates a file URI whose fragment is already decoded.
func FileURIWithFragment(path, fragment string) string {
	uri := FileURI(path)
	if fragment == "" {
		return uri
	}
	return uri + "#" + url.PathEscape(strings.TrimPrefix(fragment, "#"))
}
