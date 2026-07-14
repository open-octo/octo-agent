package main

import (
	"os"
	"strings"
	"sync/atomic"

	"github.com/open-octo/octo-agent/internal/config"
)

// uiStrings holds every user-facing native string the desktop shell shows
// outside the web UI — dialogs, the tray menu, error messages. Selected once at
// startup by the system language. Format strings keep their verbs so call sites
// can fmt.Sprintf them.
type uiStrings struct {
	trayShow, trayQuit string
	traySettings       string
	trayCheckUpdates   string
	trayUpdateAvailFmt string // "↑ Update to v%s"
	trayStarting       string
	trayBackendFmt     string // "Backend · %s"
	trayClientsFmt     string // "Connected clients: %d"
	trayChannelsFmt    string // "Configured channels: %d"

	takeoverTitle  string
	takeoverMsgFmt string // "...(pid %d)..."
	takeoverOK     string
	takeoverCancel string

	quitTitle  string
	quitMsg    string
	quitOK     string
	quitCancel string

	errTitle     string
	errBindFmt   string // "...%s...%v"
	errStopFmt   string // "...%v"
	errStartFmt  string // "...%v"
	dialogOKText string

	updTitle        string
	updFailed       string
	updLatestFmt    string // "...(v%s)."
	updAvailableFmt string // "...%s..."
	updOpen         string
}

var enStrings = uiStrings{
	trayShow:           "Show Octo",
	trayQuit:           "Quit Octo",
	traySettings:       "Settings…",
	trayCheckUpdates:   "Check for Updates…",
	trayUpdateAvailFmt: "↑ Update to v%s",
	trayStarting:       "Starting…",
	trayBackendFmt:     "Backend · %s",
	trayClientsFmt:     "Connected clients: %d",
	trayChannelsFmt:    "Configured channels: %d",

	takeoverTitle:  "Octo",
	takeoverMsgFmt: "A background Octo backend is already running (pid %d).\n\nStop it and run Octo as the hub for this machine?",
	takeoverOK:     "Stop and Continue",
	takeoverCancel: "Quit",

	quitTitle:  "Quit Octo",
	quitMsg:    "Quitting stops the Octo backend on this machine. Connected editors, browsers, and IM channels will disconnect.\n\nQuit anyway?",
	quitOK:     "Quit",
	quitCancel: "Cancel",

	errTitle:     "Octo",
	errBindFmt:   "Couldn't bind %s — another program may be using it.\n\n%v",
	errStopFmt:   "Couldn't stop the running backend: %v",
	errStartFmt:  "Couldn't start the backend: %v",
	dialogOKText: "OK",

	updTitle:        "Octo",
	updFailed:       "Couldn't check for updates. Please try again later.",
	updLatestFmt:    "You're on the latest version (v%s).",
	updAvailableFmt: "Octo %s is available.",
	updOpen:         "Open Download Page",
}

var zhStrings = uiStrings{
	trayShow:           "显示 Octo",
	trayQuit:           "退出 Octo",
	traySettings:       "设置…",
	trayCheckUpdates:   "检查更新…",
	trayUpdateAvailFmt: "↑ 更新到 v%s",
	trayStarting:       "启动中…",
	trayBackendFmt:     "后端 · %s",
	trayClientsFmt:     "已连接客户端：%d",
	trayChannelsFmt:    "已配置 channel：%d",

	takeoverTitle:  "Octo",
	takeoverMsgFmt: "已有一个 Octo 后端在后台运行（pid %d）。\n\n停止它，并让 Octo 作为本机的后端中枢？",
	takeoverOK:     "停止并继续",
	takeoverCancel: "退出",

	quitTitle:  "退出 Octo",
	quitMsg:    "退出会停止本机的 Octo 后端，已连接的编辑器、浏览器和 IM channel 都会断开。\n\n仍要退出？",
	quitOK:     "退出",
	quitCancel: "取消",

	errTitle:     "Octo",
	errBindFmt:   "无法绑定 %s —— 可能有其他程序正在占用。\n\n%v",
	errStopFmt:   "无法停止正在运行的后端：%v",
	errStartFmt:  "无法启动后端：%v",
	dialogOKText: "好",

	updTitle:        "Octo",
	updFailed:       "检查更新失败,请稍后重试。",
	updLatestFmt:    "已是最新版本(v%s)。",
	updAvailableFmt: "Octo %s 已发布。",
	updOpen:         "打开下载页",
}

// active holds the current string set. It's an atomic pointer because the tray
// refresh loop re-applies the language on its own goroutine while dialog
// callbacks read it on the UI thread.
var active atomic.Pointer[uiStrings]

// L returns the active string set (English until applyLang runs).
func L() *uiStrings {
	if s := active.Load(); s != nil {
		return s
	}
	return &enStrings
}

// applyLang re-resolves the UI language and swaps the active string set. Called
// at startup and on each tray tick, so switching language in onboarding or
// Settings (which writes ~/.octo/config.yml) is reflected in the tray/dialogs
// within a few seconds — the desktop shell follows the in-app language choice,
// not the OS.
func applyLang() {
	if resolveLang() == "zh" {
		active.Store(&zhStrings)
	} else {
		active.Store(&enStrings)
	}
}

// resolveLang prefers the user's in-app language (config.yml `language`, set in
// onboarding / Settings). Only before that's chosen does it fall back to the
// OS's preferred UI language.
func resolveLang() string {
	if cfg, err := config.Load(); err == nil {
		switch cfg.Language {
		case "zh":
			return "zh"
		case "en":
			return "en"
		}
	}
	return preferredLang()
}

// preferredLang returns "zh" or "en" from the OS's preferred UI language. It
// asks the platform (osLang: AppleLanguages on macOS, GetUserDefaultLocaleName
// on Windows) first, since a Finder/Explorer-launched app usually has no LANG;
// only when that yields nothing does it fall back to the LC_*/LANG environment
// (which is how Linux exposes it).
func preferredLang() string {
	if lang := osLang(); lang != "" {
		if strings.HasPrefix(strings.ToLower(lang), "zh") {
			return "zh"
		}
		return "en"
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if strings.HasPrefix(strings.ToLower(v), "zh") {
				return "zh"
			}
			return "en"
		}
	}
	return "en"
}
