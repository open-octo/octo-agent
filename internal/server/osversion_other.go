//go:build !darwin

package server

// OSVersion only has a consumer on darwin (see osversion_darwin.go): the
// traffic-light axis it feeds exists nowhere else, so it reports empty.
func OSVersion() string { return "" }
