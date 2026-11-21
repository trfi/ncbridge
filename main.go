package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultUA = "nodecode_cli/0.1.16+beee92a7e (Win32 6.2.9200; X86-64) xterm-256color"

func main() {
	listen := flag.String("listen", "127.0.0.1:8799", "local address to listen on")
	upstream := flag.String("upstream", "https://api.nodecode.ai", "API origin to relay to")
	flagKey := flag.String("key", "", "sk-nc key (default: NODECODE_API_KEY, then ~/.nodecode/auth.json)")
	ua := flag.String("ua", defaultUA, "User-Agent sent upstream")
	verbose := flag.Bool("v", false, "log every request")
	flag.Parse()

	parsed, err := url.Parse(*upstream)
	if err != nil || parsed.Host == "" {
		log.Fatalf("bad --upstream %q", *upstream)
	}
	addr := parsed.Host
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}

	key, err := resolveKey(*flagKey)
	if err != nil {
		log.Fatalf("%v", err)
	}

	p := &proxy{upstreamAddr: addr, key: key, userAgent: *ua, verbose: *verbose}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("/", p)

	log.Printf("bridge on http://%s/v1 -> %s (key %s…)", *listen, parsed.Host, key[:min(12, len(key))])
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 20 * time.Second}
	log.Fatal(server.ListenAndServe())
}

// resolveKey: flag, then environment, then the stores `nodecode login`
// writes (auth.json entry, or the split secrets file), free key first.
func resolveKey(flagKey string) (string, error) {
	if key := strings.TrimSpace(flagKey); key != "" {
		return key, nil
	}
	if key := strings.TrimSpace(os.Getenv("NODECODE_API_KEY")); key != "" {
		return key, nil
	}
	home, err := os.UserHomeDir()
	if err == nil {
		dir := filepath.Join(home, ".nodecode")
		if key := keyFromAuthFile(filepath.Join(dir, "auth.json")); key != "" {
			return key, nil
		}
		for _, id := range []string{"nodecode-free", "nodecode"} {
			if raw, err := os.ReadFile(filepath.Join(dir, "secrets", id)); err == nil {
				if key := strings.TrimSpace(string(raw)); key != "" {
					return key, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no sk-nc key: pass --key, set NODECODE_API_KEY or run `nodecode login`")
}

func keyFromAuthFile(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var file struct {
		APIKeys map[string]struct {
			Key string `json:"key"`
		} `json:"api_keys"`
	}
	if json.Unmarshal(raw, &file) != nil {
		return ""
	}
	for _, id := range []string{"nodecode-free", "nodecode"} {
		if key := strings.TrimSpace(file.APIKeys[id].Key); key != "" {
			return key
		}
	}
	return ""
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
