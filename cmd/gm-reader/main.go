// Command gm-reader lets Claude read (and, if you allow it, send) your Google
// Messages conversations. See README.md and SETUP.md.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/rs/zerolog"
)

func nopLog() zerolog.Logger { return zerolog.Nop() }

// version is set at build time with -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `gm-reader %s: Google Messages for Claude

Usage:
  gm-reader pair                    Pair with your phone (one time)
  gm-reader pair --refresh-cookies  Give new Google cookies without re-pairing
  gm-reader setup-claude            Add gm-reader to Claude Desktop (and show the Claude Code command)
  gm-reader serve                   Run the MCP server (Claude starts this for you)
  gm-reader settings                Show or change what Claude is allowed to do
  gm-reader automation              Labeled replies and the new-text hook (for building an assistant)
  gm-reader status                  Show pairing and cache status (no network)
  gm-reader forget                  Delete the pairing and cache from this computer
  gm-reader version                 Print the version

Run "gm-reader <command> -h" for options.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Printf(usage, version)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "pair":
		err = runPair(ctx, args)
	case "serve":
		err = runServe(ctx, args)
	case "setup-claude":
		err = runSetupClaude(args)
	case "settings":
		err = runSettings(ctx, args)
	case "automation":
		err = runAutomation(ctx, args)
	case "status":
		err = runStatus(args)
	case "forget":
		err = runForget(args)
	case "version", "--version", "-v":
		fmt.Println("gm-reader", version)
	case "help", "-h", "--help":
		fmt.Printf(usage, version)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q.\n\n", cmd)
		fmt.Printf(usage, version)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "\nError:", err)
		os.Exit(1)
	}
}

var stdin = bufio.NewReader(os.Stdin)

// ask prints a question and returns the trimmed answer.
func ask(question string) string {
	fmt.Print(question)
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

// yes asks a yes/no question; anything but y/yes counts as no.
func yes(question string) bool {
	a := strings.ToLower(ask(question + " [y/N]: "))
	return a == "y" || a == "yes"
}
