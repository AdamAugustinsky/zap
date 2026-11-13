package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Runner is the agent a Wake starts.
type Runner struct {
	// Kind is "claude" (Claude Code, held to Zap's tools), "custom" (the
	// owner's Command) or empty: none, and no chat wakes anything.
	Kind string `json:"kind,omitempty"`
	// Command is a custom runner's shell command. It gets the prompt on its
	// standard input, and in its environment ZAP_AGENT_TOKEN, ZAP_AGENT_SOCKET,
	// ZAP_CHAT, ZAP_CHAT_NAME, ZAP_MCP (the program that is Zap's MCP server
	// when run with the argument "mcp") and ZAP_MCP_CONFIG (an MCP
	// configuration file that names it so).
	Command string `json:"command,omitempty"`
}

// RunnerClaude and RunnerCustom are Runner's kinds.
const (
	RunnerClaude = "claude"
	RunnerCustom = "custom"
)

// Tools are the MCP tools Zap offers (package agent/mcp), by name.
var Tools = []string{"list_chats", "read_messages", "search_messages", "send_message", "wait_for_messages"}

// outputMax bounds what is read of a runner's output, in bytes.
const outputMax = 64 << 10

// MCPArg is the argument that makes the app's executable Zap's MCP server
// instead of the app (cmd/zapfast: `zapfast mcp`).
const MCPArg = "mcp"

// MCPPath is the program an agent starts, with MCPArg, to get Zap's MCP
// server: the app's own executable (the bundle's Contents/MacOS/zapfast).
func MCPPath() string {
	exe, err := os.Executable()
	if err != nil {
		return "zapfast"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return exe
}

// mcpConfig is the MCP configuration a run's agent loads: Zap's server and
// nothing else, holding the run's token.
func mcpConfig(r Run, mcp string) ([]byte, error) {
	type server struct {
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	return json.Marshal(map[string]map[string]server{"mcpServers": {"zap": {
		Command: mcp,
		Args:    []string{MCPArg},
		Env:     map[string]string{"ZAP_AGENT_TOKEN": r.Token, "ZAP_AGENT_SOCKET": r.Socket},
	}}})
}

// claudeArgs are Claude Code's arguments for a run: only the servers of the
// configuration file, no tool of its own (no shell, no files, no web), no
// question asked, and Zap's tools allowed by name. A message from a
// stranger reaches an agent that can do nothing but read and answer its chat.
func claudeArgs(config string) []string {
	allowed := make([]string, len(Tools))
	for i, t := range Tools {
		allowed[i] = "mcp__zap__" + t
	}
	return []string{
		"-p",
		"--strict-mcp-config", "--mcp-config", config,
		"--tools", "",
		"--allowedTools", strings.Join(allowed, ","),
		"--permission-mode", "dontAsk",
		"--max-turns", "30",
		"--output-format", "text",
		// what it reads of the chat is not kept in Claude Code's session
		// files, and no skill or command of the owner's is loaded
		"--no-session-persistence",
		"--disable-slash-commands",
	}
}

// Exec is the default Options.Run: it starts r's runner in an empty folder
// of its own, gives it the prompt on its standard input and returns what it
// printed.
func Exec(ctx context.Context, r Run) (string, error) {
	dir, err := os.MkdirTemp("", "zap-agent-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	mcp := MCPPath()
	conf, err := mcpConfig(r, mcp)
	if err != nil {
		return "", err
	}
	config := filepath.Join(dir, "mcp.json")
	if err := os.WriteFile(config, conf, 0o600); err != nil {
		return "", err
	}
	path := userPath()
	var cmd *exec.Cmd
	switch r.Runner.Kind {
	case RunnerClaude:
		bin, err := lookIn(path, "claude")
		if err != nil {
			return "", errors.New("Claude Code (claude) was not found. Install it, or choose another agent in Zap's settings")
		}
		cmd = exec.CommandContext(ctx, bin, claudeArgs(config)...)
	case RunnerCustom:
		if strings.TrimSpace(r.Runner.Command) == "" {
			return "", errors.New("the custom agent has no command")
		}
		cmd = exec.CommandContext(ctx, shell(), "-c", r.Runner.Command)
	default:
		return "", errors.New("no agent is chosen")
	}
	cmd.Dir = dir
	cmd.Env = append(cleanEnv(),
		"PATH="+path,
		"ZAP_AGENT_TOKEN="+r.Token, "ZAP_AGENT_SOCKET="+r.Socket,
		"ZAP_CHAT="+r.Chat, "ZAP_CHAT_NAME="+label(r.Name, nameMax),
		"ZAP_MCP="+mcp, "ZAP_MCP_CONFIG="+config,
	)
	cmd.Stdin = strings.NewReader(r.Prompt)
	var out, errOut limited
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = 5 * time.Second
	ownGroup(cmd)
	err = cmd.Run()
	endGroup(cmd) // nothing it started outlives the run
	if err != nil {
		// What the agent printed before failing says why.
		words := strings.TrimSpace(errOut.String())
		if words == "" {
			words = strings.TrimSpace(out.String())
		}
		if words == "" {
			words = err.Error()
		}
		return words, fmt.Errorf("the agent failed: %w", err)
	}
	return out.String(), nil
}

// limited keeps the first outputMax bytes written to it.
type limited struct{ bytes.Buffer }

func (l *limited) Write(p []byte) (int, error) {
	if room := outputMax - l.Len(); room > 0 {
		l.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func shell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	return "/bin/sh"
}

// cleanEnv is the app's environment without PATH and without anything of
// an earlier run.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "PATH=") || strings.HasPrefix(kv, "ZAP_") {
			continue
		}
		env = append(env, kv)
	}
	return env
}

var (
	pathOnce sync.Once
	pathVal  string
)

// userPath is the PATH of the owner's login shell: an app started from the
// Dock has launchd's, which lacks where agents are installed.
func userPath() string {
	pathOnce.Do(func() {
		pathVal = os.Getenv("PATH")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Between two marks, since a shell's start-up files may print.
		cmd := exec.CommandContext(ctx, shell(), "-ilc", `printf '\n<zap-path>%s</zap-path>\n' "$PATH"`)
		// A start-up file may leave something running that holds the
		// output open: the wait ends with the shell, or with the time.
		var out limited
		cmd.Stdout = &out
		cmd.WaitDelay = time.Second
		_ = cmd.Run()
		if _, rest, ok := strings.Cut(out.String(), "<zap-path>"); ok {
			if p, _, ok := strings.Cut(rest, "</zap-path>"); ok && p != "" {
				pathVal = p
			}
		}
		home, _ := os.UserHomeDir()
		for _, d := range []string{filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin"} {
			if !strings.Contains(":"+pathVal+":", ":"+d+":") {
				pathVal += ":" + d
			}
		}
	})
	return pathVal
}

// lookIn finds an executable named name in the folders of path.
func lookIn(path, name string) (string, error) {
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", exec.ErrNotFound
}
