// Command tick: timers, stopwatches and scheduled reminders for the macOS menu bar.
package main

import (
	"os"

	"github.com/lightt77/tick/internal/cli"
)

func main() { os.Exit(cli.Execute()) }
