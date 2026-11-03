# Lesson 18: MCP

## What you'll build

A hand-rolled Model Context Protocol client over stdio. It starts a server, does the handshake, lists tools (with pagination) and calls them. An adapter exposes each server tool as `mcp__<server>__<tool>`. In **deferred mode**, only a `tool_search` tool is exposed, and it loads definitions when the model looks for them. A small MCP server (`cmd/mini-mcp`) gives you something to talk to.

The demo measures a 43-tool server: about 7,000 tokens of definitions on every request when loaded up front, 88 when deferred. A live run then shows a model finding its tools, including two searches that missed.

## How the real harnesses do it

- **Claude Code.**
  - **Transports:** stdio and HTTP (SSE is deprecated). Servers are scoped local, project (`.mcp.json`) or user.
  - **Deferred by default:** "only names enter context", and schemas load through tool search. `ENABLE_TOOL_SEARCH=auto` loads schemas up front only if they fit in 10% of the window. Deferred stubs keep the tool list stable for the prompt cache (lesson 12).
  - **Output limits:** a warning at 10k tokens and a 25k limit (`MAX_MCP_OUTPUT_TOKENS`); results over 50k chars are saved to a file.
  - **Permissions:** `mcp__server__*` patterns.
- **Codex.** Tools are named `mcp__<server>__<tool>`, max 128 chars, with a SHA-1 suffix on collision or truncation. Startup timeout 30 s, call timeout 300 s. A server's `tools/list_changed` notification mid-conversation causes a cache miss. Codex has also *removed* its MCP-server mode (`codex mcp-server`, 2026-09-05) in favor of its own app-server protocol.
- **opencode.** `local` (stdio) and `remote` (StreamableHTTP, falling back to SSE, with OAuth) servers. Tools are named `server_tool`. MCP prompts become slash commands.
- **pi** rejected MCP in its original design: "MCP servers are overkill for most use cases, and they come with significant context overhead". It cited Playwright MCP at about 13.7k tokens and Chrome DevTools MCP at about 18k, 7–9% of the window. Its alternative is CLI tools with a README the model reads when it needs to. The current pi ships MCP anyway, but by default the tools aren't declared to the model at all. They're called from code (`codemode`) or loaded through `tool_search`.

**A note on protocol versions.** The newest MCP revision, `2026-07-28`, **removes the `initialize` handshake**. Every request instead carries `_meta` with the protocol version and client capabilities, and servers implement `server/discover`. This lesson implements `2025-11-25`, the last revision with the handshake, because servers have had the longest to adopt it (an inference; I didn't survey servers). Supporting the newer revision would require a different handshake.

## Design for our harness

### The wire, over stdio

Newline-delimited JSON-RPC 2.0. No embedded newlines, nothing but messages on stdout, logs on stderr.

```jsonc
→ {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"harness","version":"0.18"}}}
← {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-11-25","capabilities":{"tools":{"listChanged":false}},"serverInfo":{"name":"mini-mcp","version":"1.0.0"},"instructions":"…"}}
→ {"jsonrpc":"2.0","method":"notifications/initialized"}
→ {"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}
← {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"todo_add","description":"…","inputSchema":{…}}],"nextCursor":"…"}}
→ {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"todo_add","arguments":{"text":"buy milk"}}}
← {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"added #1"}],"isError":false}}
```

- **One reader goroutine routes responses by `id`** to waiting calls (`map[int64]chan response`). Calls from parallel tools share one pipe, so writes are serialized.
- **A dead server fails every pending and future call at once.** When `cmd.Wait()` returns, the reader closes a `dead` channel. Without that, a crashed server hangs the agent forever (`TestServerCrashSurfacesError`).
- **Errors come in two kinds.** A JSON-RPC `error` (unknown tool: `-32602`) is a Go error. A tool failure arrives as `isError: true` in a normal result, and becomes a tool result with `IsError`.
- **`readOnlyHint` annotations** make a tool read-only in the registry, so read-only MCP tools run in parallel (lesson 03).

### Names and limits

`ToolName("my server", "do.thing")` gives `mcp__my_server__do_thing`. Anything outside `[a-zA-Z0-9_-]` becomes `_`, Anthropic's rule for tool names. Names over 64 characters are cut to 55, plus `_` and 8 hex chars of a SHA-1, so two long names never collide.

### Deferred loading

```
Register(…, deferred=true) ─▶ registry has only:  tool_search {query}
model: tool_search{"query":"todo"} ─▶ every word must appear in a tool's name+description
                                    ─▶ matching tools (≤5) are registered now, and appear in the next request
```

This trades a round trip, plus a change to the tool list, for thousands of tokens on every request. Changing the tool list mid-session also changes the cached prefix (lesson 12). Claude Code avoids that by declaring deferred tools as stubs from the start (`defer_loading: true`), so the list never changes; ours does change. The optional deferral exercise explores this tradeoff.

### Configuration

`.harness/mcp.json` and `~/.config/harness/mcp.json`:

```json
{"servers": {"todo": {"command": ["/path/to/mini-mcp"], "env": {"TOKEN": "$TODO_TOKEN"}, "deferred": true}}}
```

`--mcp-deferred` defers every server. A server that fails to start is reported and skipped; the harness keeps running.

## The contract

`harness/internal/mcp`:

```go
const ProtocolVersion = "2025-11-25"
func Start(ctx context.Context, name string, argv []string, env []string) (*Client, error)
func (c *Client) ServerName() string; Instructions() string
type Tool struct { Name, Description string; InputSchema json.RawMessage; ReadOnly bool }
func (c *Client) ListTools(ctx context.Context) ([]Tool, error)        // follows nextCursor
type CallResult struct { Text string; IsError bool }
func (c *Client) Call(ctx context.Context, tool string, args json.RawMessage) (CallResult, error)
func (c *Client) Close() error
func ToolName(server, tool string) string
func Register(ctx context.Context, reg *tools.Registry, server string, c *Client, deferred bool) (int, error)
```

Provided in `support/`: `cmd/mini-mcp` (with `--synthetic N` filler tools and `--crash-after N` for tests), `cmd/mcpcost`, and `cmd/harness`, which reads `mcp.json` and adds `--mcp-deferred`. The tests build `cmd/mini-mcp`, so copy `support/` before running them.

## Build it

Continue from lesson 17 and copy this lesson’s `tests/` and `support/`. The `internal/mcp/` implementation is new; the example server and harness wiring are supplied.

1. **Transport and handshake.** Test: `go test ./internal/mcp -run TestHandshake`. Start the process with stdin and stdout pipes and launch `readLoop`. `call()` registers a channel under a new id, writes one line, then `select`s on the channel, `dead` and `ctx.Done()`. Reject a server's unsupported protocol version before sending `notifications/initialized`; this client supports only `2025-11-25`.
2. **Tools.** Tests: `-run 'TestHandshakeAndList|TestCall'`. Follow `nextCursor` until it's empty. Join text content parts; mark other kinds as omitted.
3. **Crash handling.** Fail pending calls if the server exits so the agent does not wait forever. Test: `-run TestServerCrash`.
4. **Names.** Map server tool names to model-compatible names before registering them. Test: `-run TestToolName`.
5. **Adapter and deferral.** Tests: `-run 'TestRegisterLoaded|TestDeferredSearch'`. `tool_search` is *not* read-only: it mutates the registry, so it must not run in parallel with tools that read it.

Also fixed in this lesson, in lesson 01's client: a tool call with **no arguments** (here, `todo_list {}`) used to stream an empty `arguments` string. The empty `json.RawMessage` made `json.Marshal` fail for the whole message, so the trace recorder and the session store silently dropped it. `validArgs` now stores `{}`. The fix and its test are in every lesson's copy of `internal/llm`.

## Run it

```sh
go test ./...
go build -o /tmp/mini-mcp ./cmd/mini-mcp
mkdir -p /tmp/w/.harness && echo '{"servers":{"todo":{"command":["/tmp/mini-mcp"]}}}' > /tmp/w/.harness/mcp.json
go run ./cmd/harness --root /tmp/w
> add three things to my todo list and show it
```

## Demo

`go run ./cmd/mcpcost -live` (Haiku 5.5, about $0.001):

```
MCP server with 43 tools
  loaded up front    43 definitions    6973 tokens  ██████████████████████████████████████████████████
  deferred            1 definition       88 tokens  █
  every request carries the definitions: 6885 tokens saved per request

live (anthropic/claude-haiku-5.5, deferred): "Add 'buy milk' and 'call the plumber' to the todo list, then show me the list."
  → tool_search {"query":"todo list add item"}
      Enabled: - mcp__work__todo_add: Add an item to the shared todo list.
  → tool_search {"query":"todo list show list get items"}
      No tools match todo list show list get items
  → mcp__work__todo_add {"text":"buy milk"}            → added #1
  → mcp__work__todo_add {"text":"call the plumber"}    → added #2
  → tool_search {"query":"todo"}
      Enabled: - mcp__work__todo_add … - mcp__work__todo_list: List all todo items, numbered.
  → tool_search {"query":"list tasks"}
      No tools match list tasks
  → mcp__work__todo_list {}
      1. buy milk
      2. call the plumber
```

What it shows:

- **Up front, 43 tool definitions cost 6,973 tokens on every request**, almost five times the system prompt and built-in tools together (lesson 06 measured 1,461). That's about 3.5% of a 200k window; pi's 7–9% figure is for Playwright MCP's ~13.7k tokens. Deferred, it's one 88-token definition.
- **Search quality decides whether deferral works.** Requiring *every* query word to match made "todo list show list get items" and "list tasks" miss. The model recovered with the single word "todo", at the cost of two extra round trips. The optional search exercise explores ranking by relevance.
- **The model batched** two `todo_add` calls in one step, and called the no-argument `todo_list` correctly. That call is the one that exposed the `validArgs` bug above.

## Exercises

1. Rank `tool_search` results instead of requiring every query word to match. Rerun the demo and compare the number of searches and model calls.
2. Explore cache-stable deferral: declare tools as stubs and return full schemas through `tool_search` results while leaving the original definitions unchanged. Compare cache hits with the current approach. Replacing a stub’s schema in the tool list would change the cached prefix.

## Further reading

- MCP specification (2025-11-25 lifecycle; 2026-07-28 versioning): https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle, https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning
- MCP stdio transport: https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/stdio
- Official Go SDK: https://github.com/modelcontextprotocol/go-sdk
- Claude Code MCP: https://code.claude.com/docs/en/mcp
- pi on MCP context cost: https://mariozechner.at/posts/2025-11-30-pi-coding-agent/
