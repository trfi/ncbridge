# The gate and the bridge

Why this program exists, how it answers the gate, and where it can break.

## The gate

A free key (`pays: free` in `/v1/usage`) gets `403 free_needs_nodecode` from every
client that is not `nodecode.exe`. `/v1/models` and `/v1/usage` still answer 200, so
the key itself is alive — only inference is fenced.

The fence is two checks:

1. The CLI's TLS ClientHello (TLS 1.2, fixed suite order, no SNI, no ALPN, no
   `supported_versions`).
2. A `prompt_cache_key` of 64 hex characters, plus — when `tools` is present — a
   tool named `eval` carrying the CLI's `form` / `yield_time_ms` / `strings`
   parameters.

Details and the trials behind this: gate-evidence.md.

## Design

tlsfp.go holds the ClientHello as a uTLS `ClientHelloSpec` and `dial` is the only
place a socket is opened: one connection per request, verification skipped because
the CDN presents a `*.b-cdn.net` certificate.

proxy.go rewrites two things and relays the rest:

- `prepareBody` — an absent or malformed `prompt_cache_key` becomes sha256 of the
  system prompt and first user message (plus the model), so successive turns of one
  conversation reuse one cache key. A well-formed key from the caller is kept. When
  `tools` is present and none is named `eval`, the CLI's own tool from evaltool.json
  is prepended; an empty `tools` array is dropped (the API answers a bare 400 for
  it).
- The upstream request head — headers are rebuilt in the CLI's order and casing
  (`connection`, caller headers, `authorization`, `user-agent`, `content-length`,
  `host`); the caller's `authorization` and `user-agent` are replaced by the
  bridge's.

The response is parsed only as far as the status line and headers: hop-by-hop
headers are dropped and a chunked body is decoded, because Go re-chunks whatever it
writes. Frames, bytes and JSON error bodies pass through unchanged.

Prompts, tool calls and responses are never inspected or rewritten beyond the above.

## Key resolution

`--key`, `NODECODE_API_KEY`, `~/.nodecode/auth.json` (`api_keys.nodecode-free.key`,
then `api_keys.nodecode.key`), `~/.nodecode/secrets/*`. `nodecode login` maintains
those stores. Nothing is written back and no secret is compiled in.

## Failure modes

| Symptom | Cause |
| --- | --- |
| `403 free_needs_nodecode` on every request | ClientHello drifted (uTLS upgrade, spec change) or the shape rule moved. Re-capture per gate-evidence.md. |
| `400` mentioning `prompt_cache_key` | Caller set a key that is not 64 hex. |
| `400` on any request with `tools` | The injected `eval` tool was stripped, or `tools: []` reached upstream. |
| `502 bridge_error` | Upstream dial/write/read failed — network, or the host stopped accepting TLS 1.2. |
| Model calls `eval` | Expected: the tool is real, only the CLI can execute it. |

## Pointing clients at it

Base URL `http://127.0.0.1:8799/v1`, any non-empty API key. For ZCode,
`~/.zcode/v2/provider_config.json` under a `providerRules` entry: set `api.baseUrl`
to the bridge and leave `access.apiKey` as it is.
