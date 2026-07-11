package main

import (
	"os"
	"strings"
)

// uiStrings holds every user-facing native string the desktop shell shows
// outside the web UI — dialogs, the tray menu, error messages. Selected once at
// startup by the system language. Format strings keep their verbs so call sites
// can fmt.Sprintf them.
type uiStrings struct {
	trayShow, trayQuit string
	traySettings       string
	trayStarting       string
	trayBackendFmt     string // "Backend · %s"
	trayChannelsOff    string
	trayChannelsOnNone string
	trayChannelsOnFmt  string // "Channels: on · %d (%s)"
	trayClientsFmt     string // "Connected clients: %d"

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
}

var enStrings = uiStrings{
	trayShow:           "Show Octo",
	trayQuit:           "Quit Octo",
	traySettings:       "Settings…",
	trayStarting:       "Starting…",
	trayBackendFmt:     "Backend · %s",
	trayChannelsOff:    "Channels: off",
	trayChannelsOnNone: "Channels: on · none connected",
	trayChannelsOnFmt:  "Channels: on · %d (%s)",
	trayClientsFmt:     "Connected clients: %d",

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
}

var zhStrings = uiStrings{
	trayShow:           "显示 Octo",
	trayQuit:           "退出 Octo",
	traySettings:       "设置…",
	trayStarting:       "启动中…",
	trayBackendFmt:     "后端 · %s",
	trayChannelsOff:    "Channel：未开启",
	trayChannelsOnNone: "Channel：已开启 · 暂无连接",
	trayChannelsOnFmt:  "Channel：已开启 · %d 个（%s）",
	trayClientsFmt:     "已连接客户端：%d",

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
}

// L is the active string set, chosen at startup by detectLang.
var L = enStrings

// detectLang switches L to Chinese when the system's preferred UI language is
// Chinese; everything else stays English.
func detectLang() {
	if preferredLang() == "zh" {
		L = zhStrings
	}
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
