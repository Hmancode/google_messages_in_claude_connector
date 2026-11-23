package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Hmancode/google_messages_in_claude_connector/internal/install"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/logx"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/mcpserver"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/paths"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/session"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/store"
	"github.com/Hmancode/google_messages_in_claude_connector/internal/syncer"
)

// runServe is started by Claude. stdout carries the MCP protocol, so this
// must never print to stdout; logs go to the log file.
func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	noCache := fs.Bool("no-cache", false, "keep the message cache in memory only; nothing is written to disk")
	debug := fs.Bool("debug", false, "write detailed logs (never message text)")
	chats := fs.Int("chats", 50, "how many recent conversations to keep synced")
	history := fs.Int("history", 100, "how many messages per conversation to fetch on the first sync")
	_ = fs.Parse(args)

	log, libgmLog, closer, err := logx.Open(*debug)
	if err != nil {
		return fmt.Errorf("cannot open log file: %w", err)
	}
	defer closer.Close()
	log.Info().Str("version", version).Bool("no_cache", *noCache).Msg("Starting MCP server")
	install.CleanupOld(filepath.Dir(exePath())) // copies left by an update, once no longer running

	dbPath := ""
	if !*noCache {
		if dbPath, err = paths.DatabaseFile(); err != nil {
			return err
		}
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	sess, err := session.Open()
	if err != nil {
		return err
	}
	sy := syncer.New(log, libgmLog, sess, db, syncer.Options{Conversations: *chats, History: *history})
	if err := sy.Load(); err != nil {
		// Keep serving: pairing_status explains what to do.
		if errors.Is(err, session.ErrNotPaired) {
			log.Warn().Msg("Not paired yet")
		} else {
			log.Error().Err(err).Msg("Could not load session")
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go sy.Run(ctx)

	server := mcpserver.New(sy, version)
	err = server.Run(ctx, &mcp.StdioTransport{})
	log.Info().Err(err).Msg("MCP server stopped")
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
