// Command slack-threads is the Wails entry point. Wails builds the module
// root, so this file only forwards to cmd/slack-threads' wiring.
package main

import (
	"log"

	"github.com/pavelvarganov/slack-threads/frontend"
	"github.com/pavelvarganov/slack-threads/internal/app"
)

func main() {
	if err := app.Run(frontend.Assets); err != nil {
		log.Fatalf("slack-threads: %v", err)
	}
}
