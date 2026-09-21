package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProvisioning(t *testing.T) {
	called := 0
	h := provisioning(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called++
		if r.URL.String() != "https://api.trycloudflare.com/tunnel" || (r.Host != "" && r.Host != "api.trycloudflare.com") {
			t.Fatalf("unexpected target: %s host=%s", r.URL, r.Host)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"success":true}`))}, nil
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/tunnel", nil))
	if w.Code != 200 || w.Body.String() != `{"success":true}` {
		t.Fatal(w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/anything-else", nil))
	if w.Code != 404 || called != 1 {
		t.Fatal("unexpected forwarding", w.Code, called)
	}
	for i := 0; i < 2; i++ {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("POST", "/prepare", nil))
		if w.Code != 204 || w.Body.Len() != 0 || called != 2 {
			t.Fatal("preparation leaked response or repeated request", w.Code, called)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/tunnel", nil))
	if w.Code != 200 || w.Body.String() != `{"success":true}` || called != 2 {
		t.Fatal("prepared response not used")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/tunnel", nil))
	if called != 3 {
		t.Fatal("prepared response was reused")
	}
}

func TestPrepareFailureDoesNotLeakBody(t *testing.T) {
	h := provisioning(roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("private-response"))}, nil
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/prepare", nil))
	if w.Code != 502 || strings.Contains(w.Body.String(), "private-response") {
		t.Fatal("preparation error leaked body")
	}
}

func TestForward(t *testing.T) {
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			proxy, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer proxy.Close()
			proxyDone := make(chan error, 1)
			go func() {
				conn, err := proxy.Accept()
				if err != nil {
					proxyDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewReader(conn)
				req, err := http.ReadRequest(reader)
				if err != nil {
					proxyDone <- err
					return
				}
				if req.Method != "CONNECT" || req.Host != "example.test:7844" {
					proxyDone <- fmt.Errorf("unexpected CONNECT: %+v", req)
					return
				}
				_, err = fmt.Fprintf(conn, "HTTP/1.1 %d Result\r\n\r\n", status)
				if err == nil && status == 200 {
					_, err = conn.Write([]byte("hello"))
					payload := make([]byte, 5)
					if err == nil {
						_, err = io.ReadFull(reader, payload)
					}
					if err == nil && string(payload) != "world" {
						err = fmt.Errorf("payload: %q", payload)
					}
				}
				proxyDone <- err
			}()
			client, relay := net.Pipe()
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(5 * time.Second))
			relayDone := make(chan error, 1)
			go func() { relayDone <- forward(relay, proxy.Addr().String(), "example.test:7844") }()
			if status == 200 {
				payload := make([]byte, 5)
				if _, err := io.ReadFull(client, payload); err != nil {
					t.Fatal(err)
				}
				if string(payload) != "hello" {
					t.Fatalf("payload: %q", payload)
				}
				if _, err := client.Write([]byte("world")); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-proxyDone; err != nil {
				t.Fatal(err)
			}
			if err := <-relayDone; status == 403 && err == nil {
				t.Fatal("expected proxy rejection")
			}
		})
	}
}
