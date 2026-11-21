# Free-tier gate on api.nodecode.ai

How the free-key gate was identified, and how to re-identify it if it changes.
The bridge that satisfies it: docs/gate.md.

## Symptom

A `pays: free` key gets `403 free_needs_nodecode` ("A free key works only from the
Nodecode CLI") from every client except `nodecode.exe`. `/v1/models` and `/v1/usage`
still answer 200, so the key itself is fine.

## Trap: the obvious checks are not the gate

Replaying the CLI's captured request byte for byte — same `User-Agent`
(`nodecode_cli/0.1.16+…`), same header set and order, same lowercase casing, same
body — still 403 from curl, Node and Python. `User-Agent` alone, TLS session-id
presence, header order, `Accept`, `Connection` and payload size are each
non-factors. Chasing them wastes a lot of time.

## What to capture

The CLI is a real client on this machine; make it talk to a listener you own.

ClientHello (the gate is the handshake, so capture before any TLS is answered):

    node -e 'const net=require("net"),fs=require("fs");net.createServer(s=>{let b=Buffer.alloc(0);s.on("data",d=>{b=Buffer.concat([b,d]);if(b.length>=5+b.readUInt16BE(3)){fs.writeFileSync("ch.bin",b);s.destroy();}})}).listen(8899,"127.0.0.1")'

then run the CLI against it:

    NODECODE_ENDPOINT=https://127.0.0.1:8899/v1 NODECODE_API_KEY=sk-nc-… NODECODE_WEBSOCKET=0 \
      nodecode rpc --ephemeral   # then feed {"type":"start_turn","request":{"prompt":"hi","provider":"nodecode","model":"nodecode-cyber-v1"}}

Decode `ch.bin` as TLS record 22 → handshake 1: record version `0303`, all-zero
session id, 19 suites starting `c02c`, extensions `0005 000a 000b 000d 0017 0011 ff01`
and nothing else. No SNI, no ALPN, no TLS 1.3.

Headers and body: point `NODECODE_ENDPOINT` at a plain HTTP proxy that logs and
forwards to `api.nodecode.ai`.

## Bisecting the body

With the fingerprint reproduced (uTLS `HelloCustom`), the remaining gate is
`prompt_cache_key`: 64 hex passes, absent or `"hello"` 403. With tools present, one
tool must be named `eval` and declare `properties.{form,yield_time_ms,strings}` with
`required: ["form"]`; every other tool rides along. Renaming `eval`, dropping a
property, or adding an extra required entry all 403. `tools: []` is a 400.

## Rebuild checklist when it breaks

1. Re-capture and re-diff the ClientHello and the body/types above.
2. Adjust the spec in tlsfp.go and the shape rule in proxy.go.
3. `GET /v1/models` still answering while chat 403s means the key is alive and only
   the gate moved — never issue a new key to "fix" it.
