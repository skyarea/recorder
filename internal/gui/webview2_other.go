//go:build !windows

package gui

// CheckWebView2 reports whether a WebView2 Runtime is installed. The runtime
// is a Windows component; the target platform is Windows-only, so off
// Windows there is nothing to check and nothing to tell. These keep main.go
// compiling (and `go build ./...` passing) on any host without a build tag
// at the call site.
func CheckWebView2() error { return nil }

func NotifyWebView2Missing() {}
