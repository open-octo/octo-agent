//go:build embedrg

package rgembed

import _ "embed"

//go:embed binaries/rg
var embeddedRG []byte
