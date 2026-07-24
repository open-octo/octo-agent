// Command octo-relay is the relay: a dumb pipe that brokers pairing and
// bridges end-to-end-encrypted frames between a host and its paired devices.
// It never decrypts — see package relay for the guarantee.
//
// M1a adds the hosted-deployment surface: TLS listening (--tls-cert/--tls-key)
// and /healthz for the load balancer. Push wakeups (APNs/FCM) and multi-node
// SNI-hash routing are still deferred; the design in
// dev-docs/mobile-managed-tunnel-design.md covers where they slot in.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/push"
	"github.com/open-octo/octo-agent/cmd/octo-relay/internal/relay"
)

// version is stamped by the build (-ldflags "-X main.version=..."); "dev"
// otherwise. Reported by /healthz so a rollout can be verified from the LB.
var version = "dev"

func main() {
	addr := flag.String("addr", ":8090", "listen address")
	tlsCert := flag.String("tls-cert", "", "TLS certificate file (PEM); with --tls-key, serve wss instead of plaintext")
	tlsKey := flag.String("tls-key", "", "TLS private key file (PEM)")
	apnsKeyFile := flag.String("apns-key-file", "", "APNs .p8 signing key; with the other apns flags, enables iOS wakeup pushes")
	apnsKeyID := flag.String("apns-key-id", "", "APNs key id")
	apnsTeamID := flag.String("apns-team-id", "", "APNs team id")
	apnsTopic := flag.String("apns-topic", "", "APNs topic (the app's bundle id)")
	fcmCredentials := flag.String("fcm-credentials", "", "FCM service-account JSON; enables Android wakeup pushes")
	flag.Parse()
	if err := rejectEmptyTLSFlags(flag.CommandLine); err != nil {
		log.Fatalf("[relay] %v", err)
	}

	pushers := push.Multi{}
	if *apnsKeyFile != "" || *apnsKeyID != "" || *apnsTeamID != "" || *apnsTopic != "" {
		a, err := push.NewAPNS(push.APNSConfig{KeyFile: *apnsKeyFile, KeyID: *apnsKeyID, TeamID: *apnsTeamID, Topic: *apnsTopic})
		if err != nil {
			log.Fatalf("[relay] %v", err)
		}
		pushers["apns"] = a
		log.Printf("[relay] apns wakeups enabled topic=%s", *apnsTopic)
	}
	if *fcmCredentials != "" {
		f, err := push.NewFCM(*fcmCredentials)
		if err != nil {
			log.Fatalf("[relay] %v", err)
		}
		pushers["fcm"] = f
		log.Printf("[relay] fcm wakeups enabled")
	}

	r := relay.New()
	if len(pushers) > 0 {
		r.Pusher = pushers
	}
	if err := serve(*addr, *tlsCert, *tlsKey, withHealthz(r.Handler(), version)); err != nil {
		log.Fatalf("[relay] %v", err)
	}
}

// rejectEmptyTLSFlags refuses a TLS flag that was explicitly passed with an
// empty value. systemd's ${VAR} expansion turns an unset variable into an
// empty argument (not a removed one), so a stale /etc/octo-relay/env with
// both TLS variables missing would otherwise sail into the plaintext branch —
// a silent downgrade on a public port. Not passing the flags at all (local
// development) stays plaintext as documented.
func rejectEmptyTLSFlags(fs *flag.FlagSet) error {
	var bad string
	fs.Visit(func(f *flag.Flag) {
		if (f.Name == "tls-cert" || f.Name == "tls-key") && f.Value.String() == "" {
			bad = f.Name
		}
	})
	if bad != "" {
		return fmt.Errorf("--%s was passed an empty value; unset both TLS flags for plaintext or set both to serve TLS", bad)
	}
	return nil
}

// serve listens with TLS when both cert and key are given, plaintext when
// neither is (local development), and refuses the ambiguous half-configured
// state — silently falling back to plaintext on a typo'd flag would be a
// downgrade the operator never asked for.
func serve(addr, certFile, keyFile string, h http.Handler) error {
	switch {
	case certFile != "" && keyFile != "":
		log.Printf("[relay] listening on %s (TLS)", addr)
		return http.ListenAndServeTLS(addr, certFile, keyFile, h)
	case certFile == "" && keyFile == "":
		log.Printf("[relay] listening on %s (plaintext)", addr)
		return http.ListenAndServe(addr, h)
	default:
		return fmt.Errorf("--tls-cert and --tls-key must be set together")
	}
}

// withHealthz mounts GET /healthz next to the relay endpoints. The LB health
// check and rollout verification both read it; it reports nothing beyond
// liveness and the running version (the relay stays content-free).
func withHealthz(next http.Handler, version string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "ok %s\n", version)
	})
	mux.Handle("/", next)
	return mux
}
