// octo-desktop is a nested module so the Wails v3 dependency (which pulls CGO
// and a platform webview) never enters the main module's go.mod. The parent's
// `go build ./...` / `go test ./...` skip this directory entirely, keeping the
// CLI a pure static Go binary. It reaches the parent's packages through the
// replace directive below.
module github.com/open-octo/octo-agent/cmd/octo-desktop

go 1.25.0

require (
	github.com/open-octo/octo-agent v0.0.0
	github.com/wailsapp/wails/v3 v3.0.0-alpha2.117
)

require (
	git.sr.ht/~jackmordaunt/go-toast/v2 v2.0.3 // indirect
	github.com/adrg/xdg v0.5.3 // indirect
	github.com/coder/websocket v1.8.14 // indirect
	github.com/go-ole/go-ole v1.3.0 // indirect
	github.com/godbus/dbus/v5 v5.2.2 // indirect
	github.com/gorilla/websocket v1.5.3 // indirect
	github.com/jchv/go-winloader v0.0.0-20250406163304-c1995be93bd1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/robfig/cron/v3 v3.0.1 // indirect
	github.com/tetratelabs/wazero v1.12.0 // indirect
	github.com/yuin/goldmark v1.7.16 // indirect
	golang.org/x/sys v0.44.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/open-octo/octo-agent => ../..
