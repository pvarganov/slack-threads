// Command slack-threads starts the desktop application.
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
