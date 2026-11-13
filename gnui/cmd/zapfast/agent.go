package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"zapfast/abi"
	"zapfast/agent"
	"zapfast/agent/mcp"
	"zapfast/state"
)

// agentSocket is where the running app listens for agents: in its data
// folder, beside the single-instance lock.
func agentSocket() (string, error) {
	p, err := state.AppPaths(AppID)
	if err != nil {
		return "", err
	}
	return filepath.Join(p.Data, agent.SocketName), nil
}

// runMCPIfAsked makes this run Zap's MCP server when the first argument is
// "mcp", and never returns then: an AI agent (Claude Code, Codex, opencode)
// starts the app's executable so,
//
//	claude mcp add --scope user zap -- /Applications/Zap.app/Contents/MacOS/zapfast mcp
//
// and speaks the Model Context Protocol on its standard input and output.
// It is not the app: no window, no instance lock, no log, no settings read.
// Each tool call goes to the running app over its socket (package agent),
// which answers only for the chats the owner gave AI access; with the app
// closed, or AI access off, every tool says so. $ZAP_AGENT_SOCKET names
// another socket (a demo's, a test's); $ZAP_AGENT_TOKEN is set by the app
// for a run it started, and holds the run to one chat.
//
// It must be called first in main, before the flags and the lock, as the
// updater's helper is.
func runMCPIfAsked() {
	if len(os.Args) < 2 || os.Args[1] != agent.MCPArg {
		return
	}
	socket := os.Getenv("ZAP_AGENT_SOCKET")
	if socket == "" {
		var err error
		if socket, err = agentSocket(); err != nil {
			fmt.Fprintln(os.Stderr, "zap mcp: the app's data folder:", err)
			os.Exit(1)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	s := &mcp.Server{
		Token:   os.Getenv("ZAP_AGENT_TOKEN"),
		Version: state.Version,
		Ask: func(ctx context.Context, req agent.Request) (agent.Reply, error) {
			return agent.Ask(ctx, socket, req)
		},
	}
	if err := s.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "zap mcp:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// installAgent gives s the door for AI agents (package agent): its socket
// is in the app's data folder, beside the single-instance lock, and opens
// only while Settings → AI access is on. The demo listens nowhere unless
// $ZAP_AGENT_SOCKET names a place: its chats are samples, but the app's own
// folder is not a demo's to write in.
func installAgent(s *state.State, wake func(), demo bool) {
	socket := os.Getenv("ZAP_AGENT_SOCKET")
	if !demo {
		var err error
		if socket, err = agentSocket(); err != nil {
			slog.Warn("zapfast: no place for the agents' socket", "err", err)
		}
	}
	s.Agent = agent.New(agent.Options{Socket: socket, Changed: wake})
}

// History and SearchHistory are core.History, read from the account's
// archive on the engine's goroutine.
func (g *goConn) History(ctx context.Context, chat, before string, limit int) ([]abi.Message, bool, error) {
	return g.eng.History(ctx, chat, before, limit)
}

func (g *goConn) SearchHistory(ctx context.Context, chat, query string, limit int) ([]abi.Message, error) {
	return g.eng.SearchHistory(ctx, chat, query, limit)
}
