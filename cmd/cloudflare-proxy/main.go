// cloudflare-proxy forwards cloudflared's encrypted HTTP/2 connection via a local HTTP proxy.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"
)

func main() {
	proxyURL := &url.URL{Scheme: "http", Host: "127.0.0.1:7890"}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	go func() {
		server := &http.Server{Addr: "127.0.0.1:17845", Handler: provisioning(transport), ReadHeaderTimeout: 5 * time.Second}
		log.Fatal(server.ListenAndServe())
	}()
	listener, err := net.Listen("tcp", "127.0.0.1:17844")
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	log.Print("Cloudflare relay: 127.0.0.1:17844 -> 127.0.0.1:7890 -> region1.v2.argotunnel.com:7844")
	for {
		client, err := listener.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go func() {
			if err := forward(client, "127.0.0.1:7890", "region1.v2.argotunnel.com:7844"); err != nil {
				log.Printf("relay: %v", err)
			}
		}()
	}
}

func provisioning(transport http.RoundTripper) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(&url.URL{Scheme: "https", Host: "api.trycloudflare.com"})
		},
		Transport: transport,
	}
	// cloudflared allows only 15s for provisioning; prepare its one-use response
	// before launch when the outbound proxy takes longer. Credentials stay in memory.
	var mu sync.Mutex
	var prepared []byte
	mux := http.NewServeMux()
	mux.HandleFunc("POST /prepare", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if prepared != nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://api.trycloudflare.com/tunnel", nil)
		if err != nil {
			http.Error(w, "Cannot prepare tunnel", http.StatusBadGateway)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		client := &http.Client{Transport: transport, Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "Tunnel preparation request failed", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
		if err != nil || resp.StatusCode != http.StatusOK || len(body) > 1<<20 || !json.Valid(body) {
			http.Error(w, "Tunnel preparation response invalid", http.StatusBadGateway)
			return
		}
		prepared = body
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /tunnel", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body := prepared
		prepared = nil
		mu.Unlock()
		if body == nil {
			proxy.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(body)
	})
	return mux
}

func forward(client net.Conn, proxyAddress, target string) error {
	defer client.Close()
	upstream, err := net.DialTimeout("tcp", proxyAddress, 10*time.Second)
	if err != nil {
		return err
	}
	defer upstream.Close()
	if err := upstream.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(upstream, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		return err
	}
	reader := bufio.NewReader(upstream)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy CONNECT: %s", response.Status)
	}
	if err := upstream.SetDeadline(time.Time{}); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = io.Copy(upstream, client)
		_ = upstream.Close()
	}()
	_, err = io.Copy(client, reader)
	_ = client.Close()
	<-done
	return err
}
