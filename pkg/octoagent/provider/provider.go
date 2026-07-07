// Package provider aliases octo-agent's provider-client construction so
// external consumers can build a Sender from plain values without touching
// ~/.octo/config.yml or any other local file.
package provider

import (
	"github.com/open-octo/octo-agent/internal/app"
	"github.com/open-octo/octo-agent/pkg/octoagent"
)

// Options is the value object used to construct a Sender.
type Options = app.SenderOptions

// NewSender builds an agent.Sender from the provided options.
// It performs no file I/O and reads no environment variables beyond what the
// underlying provider client needs for its own configuration.
func NewSender(opts Options) (octoagent.Sender, error) { return app.NewSender(opts) }
