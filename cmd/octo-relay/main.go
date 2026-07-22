// Command octo-relay is the PoC relay: a single-node dumb pipe that brokers
// pairing and bridges end-to-end-encrypted frames between a host and its paired
// devices. It never decrypts — see package relay for the guarantee.
//
// This is the transport-core proof only. Push wakeups (APNs/FCM) and multi-node
// SNI-hash routing are deferred; the design in
// dev-docs/mobile-managed-tunnel-design.md covers where they slot in.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/relay"
)

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	flag.Parse()

	r := relay.New()
	log.Printf("[relay] listening on %s", *addr)
	if err := http.ListenAndServe(*addr, r.Handler()); err != nil {
		log.Fatalf("[relay] %v", err)
	}
}
