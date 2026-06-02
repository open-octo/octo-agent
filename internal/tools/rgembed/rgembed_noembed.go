//go:build !embedrg

package rgembed

// embeddedRG is nil when the embedrg build tag is not set.
// CI builds without the tag so go:embed does not require binaries/rg to exist.
var embeddedRG []byte
