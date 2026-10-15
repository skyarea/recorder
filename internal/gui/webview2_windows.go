//go:build windows

package gui

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// webview2InstallKey is where the Edge updater records the evergreen
// WebView2 Runtime, the same GUID the loader itself looks for (mirrors
// go-webview2's find_dll_installed.go). WOW6432Node is covered by reading
// with the 32-bit view as well: a 32-bit-only install lands there.
const webview2InstallKey = `SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// webview2DownloadURL is the official redistributable page the user is sent
// to when the runtime is missing.
const webview2DownloadURL = `https://developer.microsoft.com/microsoft-edge/webview2/`

// CheckWebView2 reports whether a WebView2 Runtime this process could load
// is installed. Wails fails with a raw COM error when it is not; the window
// never appears and a double-click looks like a crash. main.go calls this
// before gui.Run and shows the answer in a MessageBox.
func CheckWebView2() error {
	for _, view := range []uint32{registry.WOW64_64KEY, registry.WOW64_32KEY} {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE, webview2InstallKey, registry.QUERY_VALUE|view)
		if err == nil {
			k.Close()
			return nil
		}
		if !errors.Is(err, registry.ErrNotExist) {
			return err
		}
	}
	// Per-user (non-admin) installs record the same key under HKCU.
	if k, err := registry.OpenKey(registry.CURRENT_USER, webview2InstallKey, registry.QUERY_VALUE); err == nil {
		_ = k.Close()
		return nil
	}
	return errors.New("WebView2 Runtime not found")
}

// NotifyWebView2Missing tells the user what is wrong and what to do about
// it, in a dialog — there is no window to show it in, and a console is not
// attached to a double-clicked GUI binary.
func NotifyWebView2Missing() {
	text := windows.StringToUTF16Ptr(
		"本程序需要 Microsoft Edge WebView2 运行库，但此电脑上没有安装。\n\n" +
			"请安装后重新打开：\n" + webview2DownloadURL)
	caption := windows.StringToUTF16Ptr("PDF 阅读进度 · 笔记")
	_, _ = windows.MessageBox(0, text, caption, windows.MB_OK|windows.MB_ICONERROR)
}
