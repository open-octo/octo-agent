package main

import "testing"

func TestDaemonDialAddr(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, "127.0.0.1:8088"},
		{"separate flag", []string{"-addr", "127.0.0.1:3000"}, "127.0.0.1:3000"},
		{"long separate flag", []string{"--addr", "127.0.0.1:3000"}, "127.0.0.1:3000"},
		{"equals form", []string{"-addr=127.0.0.1:9000"}, "127.0.0.1:9000"},
		{"long equals form", []string{"--addr=127.0.0.1:9000"}, "127.0.0.1:9000"},
		{"wildcard host dialed on loopback", []string{"-addr", ":8080"}, "127.0.0.1:8080"},
		{"0.0.0.0 dialed on loopback", []string{"-addr", "0.0.0.0:8081"}, "127.0.0.1:8081"},
		{"ipv6 wildcard dialed on loopback", []string{"-addr", "[::]:8082"}, "127.0.0.1:8082"},
		{"other flags ignored", []string{"--no-tools", "-addr", "127.0.0.1:7000", "--cors", "*"}, "127.0.0.1:7000"},
		{"last addr wins", []string{"-addr", "127.0.0.1:1111", "-addr", "127.0.0.1:2222"}, "127.0.0.1:2222"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := daemonDialAddr(tc.args); got != tc.want {
				t.Errorf("daemonDialAddr(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
