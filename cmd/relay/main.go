// Command relay is the stateless session relay. It forwards encrypted
// frames between a host and its guests over WebSocket and never holds
// plaintext. See prd.md §8.1. Implementation lands in issue #3.
package main

import (
	"fmt"
	"os"
)

var version = "0.0.1-dev"

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-v") {
		fmt.Println("tandem-relay", version)
		return
	}
	fmt.Fprintln(os.Stderr, "tandem-relay: implementation lands in issue #3")
	os.Exit(2)
}
