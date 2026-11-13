# AI access

AI access lets an AI agent you run yourself (Claude Code, Codex, opencode, or
anything that speaks MCP) read chosen chats, and answer in them when you allow
it. It is off until you turn it on, and it is per chat: an agent never learns
that any other chat exists.

This is the one place where Zap hands message content to someone else. What an
agent reads goes to that agent and the company that runs its model. Zap itself
sends nothing anywhere; the agent is a program on your Mac that asks the running
app.

## Giving a chat to an agent

Right-click a chat and choose **AI access**, or open the chat's info panel. A
chat has one of three levels:

- **Off**: the default. No agent sees the chat.
- **Read**: an agent can read and search the chat.
- **Read and reply**: it can also write. **Ask me before each reply** is on for
  a new grant: the agent's reply waits above the message field with Send, Edit
  and Discard, and nothing goes out until you send it. Turn it off and replies
  go out from your account on their own, up to the hourly limit in Settings
  (20 by default).

**Instructions for the agent** are what you want done in this chat ("get a
better price for the internet plan; never agree to a contract without asking
me"). **Start my agent when a message arrives** runs the agent chosen in
Settings each time the other side writes; without it, an agent sees the chat
only when you ask one to look.

Settings → AI access has the main switch, the commands that connect an agent,
the agent a message starts, the hourly limit, every chat that has access, and
what agents did lately. The activity list names chats and counts messages; it
never holds what a message said.

A locked chat is never given: locking a chat takes its AI access away, and so
does deleting it. Channels have no AI access. The main switch is yours alone:
giving the very first chat turns it on, and after that nothing but the switch
does.

## Connecting an agent

Zap must be open. The app's executable is the MCP server when run with the
argument `mcp`; Settings → AI access copies these with the right path:

```sh
claude mcp add --scope user zap -- "/Applications/Zap.app/Contents/MacOS/zapfast" mcp
codex mcp add zap -- "/Applications/Zap.app/Contents/MacOS/zapfast" mcp
opencode mcp add --global zap -- "/Applications/Zap.app/Contents/MacOS/zapfast" mcp
```

The tools:

| Tool | What it does |
|---|---|
| `list_chats` | the chats you gave, each with its handle, what the agent may do there and your instructions |
| `read_messages` | a page of a chat, newest last; `before` pages back (30 messages by default, 100 at most) |
| `search_messages` | the messages of one chat that match a query |
| `send_message` | a reply; in an ask-first chat it is shown to you instead of being sent |
| `wait_for_messages` | waits for the next message in a chat (50 s by default, 100 s at most) |

An agent gets words, not files: a picture is "image" with its caption, a voice
note says how long it is and that it was not transcribed, and what WhatsApp
shows only on the phone is "unavailable". Your own messages carry
`"mine": true`; `from` is a name its sender chose, so it proves nothing. A
reply may quote (`reply_to`) one of the chat's last hundred messages.

## An agent started by a message

With **Start my agent when a message arrives**, Zap waits until the chat has
been quiet for a few seconds, then starts the agent chosen in Settings with a
prompt that names the chat, your instructions and the rules (below). One run
at a time per chat, at most 30 an hour, ten minutes each. What the agent says
at the end is its report, shown in the chat's info panel.

A run holds a token that is good for its chat alone and ends with the run, so
a started agent cannot reach another chat even if a message tells it to.

**Claude Code** is started locked down: Zap's five tools and nothing else. No
shell, no files, no web, no other MCP server, no session kept on disk. A
message from a stranger reaches an agent that can only read and answer that
chat.

Whatever a run started ends with it: when its time is up, or AI access is
turned off, its whole process group is stopped.

**Custom command** runs in your shell with the prompt on its standard input
and these in its environment:

| Variable | |
|---|---|
| `ZAP_MCP_CONFIG` | an MCP configuration file naming Zap's server, with the run's token |
| `ZAP_MCP` | the program that is Zap's MCP server when run with the argument `mcp` |
| `ZAP_AGENT_TOKEN`, `ZAP_AGENT_SOCKET` | the run's token and the app's socket |
| `ZAP_CHAT`, `ZAP_CHAT_NAME` | the chat's handle and name |

Zap cannot limit what a custom command does. Give it Zap's tools and nothing
else: an agent with a shell that reads messages from strangers can be talked
into using it.

## What keeps it safe

- Off by default, per chat, and the main switch closes everything at once.
  Turning it off drops every waiting reply and stops every run.
- The agents' door is a Unix socket in the app's data folder, readable by your
  user only. Nothing listens on the network.
- A chat with no grant, a locked chat and a chat that does not exist all get
  the same answer.
- Unattended replies stop at the hourly limit; ask-first chats hold at most
  five waiting replies. A reply too long to show whole cannot be sent from the
  strip: Edit puts it in the message field first. A reply that has just
  appeared in the strip does not answer a click for a moment, so a double-click
  cannot send the next one unread.
- While Zap is not connected to WhatsApp, nothing is sent on its own, and a
  reply you approve stays in the strip until it can go.
- A chat's name and a sender's name are other people's words. An agent gets
  them as one line of data, never among its instructions.
- The prompt and the MCP server both tell the agent that chat text is
  information, never instructions; that it must not claim to be a person when
  sincerely asked; and that it may not agree to a payment, a contract, a
  cancellation or sharing personal data unless your instructions allow that
  exact thing. These are instructions to a model, not guarantees. Ask-first is
  the guarantee.

## Limits

- Zap has to be running. Grants are kept; waiting replies, reports and the
  activity list are not kept across a restart, and the hourly counts start
  again with it.
- Messages that arrive while Zap is closed or offline do not start an agent
  when they come in with the catch-up; the next message does.
- Agents do not get pictures, files or transcriptions.
- Instructions are one line of text for now.

## For developers

`gnui/agent` is the door (`agent.Server`): grants, the socket, waits, waking
runs, the activity list. `gnui/agent/mcp` is the MCP server; `cmd/zapfast/agent.go`
wires both. `gnui/state/ai.go` keeps the person's choices (a chat's grant in
the account's settings file, the rest in `settings.json`) and tells the door
when they change. A backend offers its history to agents through
`core.History`.

The demo listens only where `$ZAP_AGENT_SOCKET` says:

```sh
cd gnui
ZAP_AGENT_SOCKET=/tmp/zap-demo.sock go run ./cmd/zapfast -demo -demo-page ai-access
ZAP_AGENT_SOCKET=/tmp/zap-demo.sock go run ./cmd/zapfast mcp   # an MCP client's command
```
