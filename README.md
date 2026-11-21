# ncbridge

Local OpenAI-compatible bridge that makes a **free** [Nodecode](https://nodecode.ai)
API key work outside the Nodecode CLI — ZCode, Cursor, scripts, any OpenAI client.

The free tier of `api.nodecode.ai` refuses every client that is not the CLI itself
(`403 free_needs_nodecode`). This bridge replays exactly what the API checks, then
relays everything else untouched.

    ncbridge.exe            # 127.0.0.1:8799, key taken from the Nodecode stores
    ncbridge.exe -v         # log each request
    ncbridge.exe --key sk-nc-… --listen 127.0.0.1:9001

Point any client at base URL `http://127.0.0.1:8799/v1` with any non-empty API key.

    curl http://127.0.0.1:8799/v1/chat/completions \
      -H "Content-Type: application/json" \
      -d '{"model":"nodecode-cyber-v1","messages":[{"role":"user","content":"hi"}]}'

## What the gate checks

Two things, independently; either missing is a 403:

1. **TLS ClientHello** — TLS 1.2 only, 19 suites in OpenSSL order, no SNI, no ALPN,
   no `supported_versions`. curl, Node and Python fail against the API even with the
   CLI's exact headers, header order and body.
2. **Request shape** — a `prompt_cache_key` of 64 hex characters, and, when `tools`
   is present, a tool named `eval` whose `parameters` declare
   `form` / `yield_time_ms` / `strings` with `required: ["form"]`.

Nothing else matters: `User-Agent`, header casing and order, `temperature`, `top_p`,
`reasoning_effort`, `tool_choice`, `max_tokens`, payload size.

## What the bridge does

- `tlsfp.go` — reproduces that ClientHello with [uTLS](https://github.com/refraction-networking/utls).
- `proxy.go` — supplies a stable `prompt_cache_key` (sha256 of the conversation's
  head, so a growing conversation keeps one cache key) and, when the caller sends
  tools, prepends the CLI's `eval` tool from `evaltool.json`. The caller's tools
  follow untouched and the model does call them.
- Streaming SSE, `/v1/models`, `/v1/usage` and error bodies are relayed byte for byte,
  chunked framing included.

## Key

Resolution order: `--key`, `NODECODE_API_KEY`, `~/.nodecode/auth.json`
(`api_keys.nodecode-free.key`, then `api_keys.nodecode.key`), `~/.nodecode/secrets/*`.
`nodecode login` fills the auth file; a free key from <https://nodecode.ai/cli> works
once the account has topped up.

No secret lives in this repository.

## Build

Go 1.24+:

    go build -o ncbridge.exe .

The only dependency is uTLS. `run.cmd` starts the binary minimised on Windows; a
startup shortcut keeps it around.

## Limits

- Free tier only: Cyber v1, 256K context, two requests at once.
- The CLI's own agent loop is not emulated. A model may call the injected `eval`
  tool; only the CLI can execute it, so clients without an `eval` handler will
  surface an error for that call — harmless otherwise.
- The gate is server-side and undocumented, so it can change. If requests start
  failing with `free_needs_nodecode`, docs/gate-evidence.md holds the capture and
  bisect procedure that found it.

## Docs

- docs/gate.md — design, operation, failure modes.
- docs/gate-evidence.md — how the gate was identified, and how to re-identify it.

## License

MIT.
