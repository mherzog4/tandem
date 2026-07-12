// Command relay is the stateless session relay. It forwards opaque
// frames between a host and its guests over WebSocket and never holds
// plaintext. See prd.md §8.1 and internal/relay.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/mherzog4/tandem/internal/relay"
)

var version = "0.0.1-dev"

func main() {
	// Env fallbacks (TANDEM_ADDR, TANDEM_BASE_URL) so the container can be
	// configured without a shell — distroless has none. Flags win.
	addr := flag.String("addr", envOr("TANDEM_ADDR", ":8080"), "listen address")
	baseURL := flag.String("base-url", envOr("TANDEM_BASE_URL", "http://localhost:8080"), "external URL used in join links")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("tandem-relay", version)
		os.Exit(0)
	}
	log.Printf("tandem-relay %s listening on %s (base-url %s)", version, *addr, *baseURL)
	log.Fatal(http.ListenAndServe(*addr, relay.NewServer(*baseURL)))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
