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
	addr := flag.String("addr", ":8080", "listen address")
	baseURL := flag.String("base-url", "http://localhost:8080", "external URL used in join links")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println("tandem-relay", version)
		os.Exit(0)
	}
	log.Printf("tandem-relay %s listening on %s", version, *addr)
	log.Fatal(http.ListenAndServe(*addr, relay.NewServer(*baseURL)))
}
