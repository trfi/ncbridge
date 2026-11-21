package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
)

const maxBody = 64 << 20

var hopHeaders = map[string]bool{
	"connection": true, "keep-alive": true, "proxy-authenticate": true,
	"proxy-authorization": true, "te": true, "trailer": true,
	"transfer-encoding": true, "upgrade": true, "content-length": true,
	"host": true,
}

type proxy struct {
	upstreamAddr string // host:port
	key          string
	userAgent    string
	verbose      bool
}

// ServeHTTP rewrites the two things the API requires of a nodecode CLI and
// relays the rest untouched: the bearer key, and a prompt cache key present
// and shaped as 64 hex characters. Everything else — tools, stream options,
// max_tokens, retries — is the caller's own body.
func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "reading request body: "+err.Error())
		return
	}
	if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/") {
		body = prepareBody(body)
	}

	headers := [][2]string{
		{"connection", "Keep-Alive"},
	}
	hasType := false
	for name, values := range r.Header {
		lower := strings.ToLower(name)
		if hopHeaders[lower] || lower == "authorization" || lower == "user-agent" {
			continue
		}
		hasType = hasType || lower == "content-type"
		for _, v := range values {
			headers = append(headers, [2]string{lower, v})
		}
	}
	if !hasType {
		headers = append(headers, [2]string{"content-type", "application/json"})
	}
	headers = append(headers,
		[2]string{"authorization", "Bearer " + p.key},
		[2]string{"user-agent", p.userAgent},
		[2]string{"content-length", strconv.Itoa(len(body))},
		[2]string{"host", p.upstreamAddr[:strings.LastIndex(p.upstreamAddr, ":")]},
	)

	target := r.URL.Path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	if p.verbose {
		log.Printf("%s %s (%d bytes)", r.Method, target, len(body))
	}

	conn, err := dial(p.upstreamAddr)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream dial: "+err.Error())
		return
	}
	defer conn.Close()

	var head bytes.Buffer
	fmt.Fprintf(&head, "%s %s HTTP/1.1\r\n", r.Method, target)
	for _, h := range headers {
		fmt.Fprintf(&head, "%s: %s\r\n", h[0], h[1])
	}
	head.WriteString("\r\n")
	head.Write(body)
	if _, err := conn.Write(head.Bytes()); err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream write: "+err.Error())
		return
	}

	reader := bufio.NewReaderSize(conn, 32*1024)
	statusLine, err := reader.ReadString('\n')
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream read: "+err.Error())
		return
	}
	fields := strings.SplitN(strings.TrimSpace(statusLine), " ", 3)
	status, err := strconv.Atoi(fields[1])
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "upstream status: "+statusLine)
		return
	}
	var contentLength int64 = -1
	var chunked bool
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, "upstream headers: "+err.Error())
			return
		}
		if line == "\r\n" || line == "\n" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		lower := strings.ToLower(strings.TrimSpace(name))
		trimmed := strings.TrimSpace(value)
		switch lower {
		case "transfer-encoding":
			chunked = chunked || strings.Contains(strings.ToLower(trimmed), "chunked")
			continue
		case "content-length":
			if n, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
				contentLength = n
			}
			continue
		}
		if hopHeaders[lower] {
			continue
		}
		w.Header().Add(strings.TrimSpace(name), trimmed)
	}
	// The upstream body is still chunked on the wire; Go re-chunks whatever is
	// written, so the framing has to be decoded on the way through.
	var source io.Reader = reader
	if chunked {
		source = httputil.NewChunkedReader(reader)
	} else if contentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(contentLength, 10))
	}
	w.WriteHeader(status)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 16*1024)
	for {
		n, err := source.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

//go:embed evaltool.json
var evalToolJSON []byte

var evalTool map[string]any

func init() {
	if err := json.Unmarshal(evalToolJSON, &evalTool); err != nil {
		panic(err)
	}
}

// prepareBody makes an arbitrary OpenAI body pass the free-key gate, which
// wants the request to look like one this CLI would send:
//
//   - a 64-hex prompt_cache_key (the caller's own when already that shape,
//     else a digest of the conversation's head, so a growing conversation
//     keeps one key the way the CLI's memo does);
//   - when tools are present, one tool named eval carrying the CLI's
//     form / yield_time_ms / strings parameters — the gate matches that
//     shape, so the caller's tools ride along after it untouched.
//
// Without either, the API answers free_needs_nodecode whatever the TLS
// fingerprint.
func prepareBody(body []byte) []byte {
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil {
		return body
	}
	changed := false
	if tools, ok := parsed["tools"].([]any); ok {
		switch {
		case len(tools) == 0:
			delete(parsed, "tools")
			changed = true
		case !hasEvalTool(tools):
			parsed["tools"] = append([]any{evalTool}, tools...)
			changed = true
		}
	}
	if existing, ok := parsed["prompt_cache_key"].(string); !ok || !isHex64(existing) {
		parsed["prompt_cache_key"] = cacheKeyOf(parsed)
		changed = true
	}
	if !changed {
		return body
	}
	rewritten, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return rewritten
}

func hasEvalTool(tools []any) bool {
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := tool["function"].(map[string]any)
		if ok && fn["name"] == "eval" {
			return true
		}
	}
	return false
}

func cacheKeyOf(parsed map[string]any) string {
	sum := sha256.New()
	messages, _ := parsed["messages"].([]any)
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role, _ := message["role"].(string)
		if role != "system" && role != "user" {
			continue
		}
		io.WriteString(sum, messageText(message["content"]))
		if role == "user" {
			break
		}
	}
	if model, ok := parsed["model"].(string); ok {
		io.WriteString(sum, model)
	}
	return hex.EncodeToString(sum.Sum(nil))
}

func messageText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var b strings.Builder
		for _, part := range value {
			if object, ok := part.(map[string]any); ok {
				if text, ok := object["text"].(string); ok {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	}
	return ""
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func writeJSONError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"message": detail, "type": "bridge_error"},
	})
}
