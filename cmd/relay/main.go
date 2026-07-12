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
	// Env fallbacks so the shell-less distroless container is configurable.
	// Listen address precedence: PORT (Railway/Render/Cloud Run inject it)
	// → TANDEM_ADDR → :8080. Flags still win over all of them.
	addr := flag.String("addr", listenAddr(), "listen address")
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

// listenAddr resolves the listen address, preferring the platform PORT
// convention (Railway/Render/Cloud Run) over TANDEM_ADDR.
func listenAddr() string {
	if p := os.Getenv("PORT"); p != "" {
		return ":" + p
	}
	return envOr("TANDEM_ADDR", ":8080")
}
