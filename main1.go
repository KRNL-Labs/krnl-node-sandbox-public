package main

import (
	//"context"
	//"encoding/binary"
	//"errors"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	//"bytes"

	"github.com/gabkov/krnl-node/rpc"
)

type testService struct{}

type echoArgs struct {
	S string
}

type echoResult struct {
	String string
	Int    int
	Args   *echoArgs
}

func (s *testService) Null() any {
	return nil
}

func (s *testService) Echo(str string, i int, args *echoArgs) echoResult {
	return echoResult{str, i, args}
}

type notificationTestService struct {
	unsubscribed            chan string
	gotHangSubscriptionReq  chan struct{}
	unblockHangSubscription chan struct{}
}

func (s *notificationTestService) Echo(i int) int {
	return i
}

func (s *notificationTestService) Unsubscribe(subid string) {
	if s.unsubscribed != nil {
		s.unsubscribed <- subid
	}
}

func newTestServer() *rpc.Server {
	server := rpc.NewServer()
	//server.idgen = sequentialIDGenerator()
	if err := server.RegisterName("test", new(testService)); err != nil {
		panic(err)
	}
	if err := server.RegisterName("nftest", new(notificationTestService)); err != nil {
		panic(err)
	}
	return server
}

// A Server is an HTTP server listening on a system-chosen port on the
// local loopback interface, for use in end-to-end HTTP tests.
type Server struct {
	URL      string // base URL of form http://ipaddr:port with no trailing slash
	Listener net.Listener

	// EnableHTTP2 controls whether HTTP/2 is enabled
	// on the server. It must be set between calling
	// NewUnstartedServer and calling Server.StartTLS.
	EnableHTTP2 bool

	// TLS is the optional TLS configuration, populated with a new config
	// after TLS is started. If set on an unstarted server before StartTLS
	// is called, existing fields are copied into the new config.
	TLS *tls.Config

	// Config may be changed after calling NewUnstartedServer and
	// before Start or StartTLS.
	Config *http.Server

	// certificate is a parsed version of the TLS config certificate, if present.
	certificate *x509.Certificate

	// wg counts the number of outstanding HTTP requests on this server.
	// Close blocks until all requests are finished.
	wg sync.WaitGroup

	mu     sync.Mutex // guards closed and conns
	closed bool
	conns  map[net.Conn]http.ConnState // except terminal states

	// client is configured for use with the server.
	// Its transport is automatically closed when Close is called.
	client *http.Client
}

// NewServer starts and returns a new Server.
// The caller should call Close when finished, to shut it down.
func NewServer(handler http.Handler) *Server {
	ts := NewUnstartedServer(handler)
	ts.Start()
	return ts
}

// NewUnstartedServer returns a new Server but doesn't start it.
//
// After changing its configuration, the caller should call Start or
// StartTLS.
//
// The caller should call Close when finished, to shut it down.
func NewUnstartedServer(handler http.Handler) *Server {
	return &Server{
		Listener: newLocalListener(),
		Config:   &http.Server{Handler: handler},
	}
}

// When debugging a particular http server-based test,
// this flag lets you run
//
//	go test -run=BrokenTest -httptest.serve=127.0.0.1:8000
//
// to start the broken server so you can interact with it manually.
// We only register this flag if it looks like the caller knows about it
// and is trying to use it as we don't want to pollute flags and this
// isn't really part of our API. Don't depend on this.
var serveFlag string

func newLocalListener() net.Listener {
	if serveFlag != "" {
		l, err := net.Listen("tcp", serveFlag)
		if err != nil {
			panic(fmt.Sprintf("httptest: failed to listen on %v: %v", serveFlag, err))
		}
		return l
	}
	l, err := net.Listen("tcp", "127.0.0.1:1234")
	if err != nil {
		if l, err = net.Listen("tcp6", "[::1]:0"); err != nil {
			panic(fmt.Sprintf("httptest: failed to listen on a port: %v", err))
		}
	}
	return l
}

// Start starts a server from NewUnstartedServer.
func (s *Server) Start() {
	if s.URL != "" {
		panic("Server already started")
	}
	if s.client == nil {
		s.client = &http.Client{Transport: &http.Transport{}}
	}
	s.URL = "http://" + s.Listener.Addr().String()
	s.wrap()
	s.goServe()
}

func (s *Server) goServe() {
	s.wg.Add(1)
	//go func() {
	defer s.wg.Done()
	s.Config.Serve(s.Listener)
	//}()
}

// wrap installs the connection state-tracking hook to know which
// connections are idle.
func (s *Server) wrap() {
	oldHook := s.Config.ConnState
	s.Config.ConnState = func(c net.Conn, cs http.ConnState) {
		s.mu.Lock()
		defer s.mu.Unlock()

		switch cs {
		case http.StateNew:
			if _, exists := s.conns[c]; exists {
				panic("invalid state transition")
			}
			if s.conns == nil {
				s.conns = make(map[net.Conn]http.ConnState)
			}
			// Add c to the set of tracked conns and increment it to the
			// waitgroup.
			s.wg.Add(1)
			s.conns[c] = cs
			if s.closed {
				// Probably just a socket-late-binding dial from
				// the default transport that lost the race (and
				// thus this connection is now idle and will
				// never be used).
				s.closeConn(c)
			}
		case http.StateActive:
			if oldState, ok := s.conns[c]; ok {
				if oldState != http.StateNew && oldState != http.StateIdle {
					panic("invalid state transition")
				}
				s.conns[c] = cs
			}
		case http.StateIdle:
			if oldState, ok := s.conns[c]; ok {
				if oldState != http.StateActive {
					panic("invalid state transition")
				}
				s.conns[c] = cs
			}
			if s.closed {
				s.closeConn(c)
			}
		case http.StateHijacked, http.StateClosed:
			// Remove c from the set of tracked conns and decrement it from the
			// waitgroup, unless it was previously removed.
			if _, ok := s.conns[c]; ok {
				delete(s.conns, c)
				// Keep Close from returning until the user's ConnState hook
				// (if any) finishes.
				defer s.wg.Done()
			}
		}
		if oldHook != nil {
			oldHook(c, cs)
		}
	}
}

// closeConn closes c.
// s.mu must be held.
func (s *Server) closeConn(c net.Conn) { s.closeConnChan(c, nil) }

// closeConnChan is like closeConn, but takes an optional channel to receive a value
// when the goroutine closing c is done.
func (s *Server) closeConnChan(c net.Conn, done chan<- struct{}) {
	c.Close()
	if done != nil {
		done <- struct{}{}
	}
}

// Close shuts down the server and blocks until all outstanding
// requests on this server have completed.
func (s *Server) Close() {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.Listener.Close()
		s.Config.SetKeepAlivesEnabled(false)
		for c, st := range s.conns {
			// Force-close any idle connections (those between
			// requests) and new connections (those which connected
			// but never sent a request). StateNew connections are
			// super rare and have only been seen (in
			// previously-flaky tests) in the case of
			// socket-late-binding races from the http Client
			// dialing this server and then getting an idle
			// connection before the dial completed. There is thus
			// a connected connection in StateNew with no
			// associated Request. We only close StateIdle and
			// StateNew because they're not doing anything. It's
			// possible StateNew is about to do something in a few
			// milliseconds, but a previous CL to check again in a
			// few milliseconds wasn't liked (early versions of
			// https://golang.org/cl/15151) so now we just
			// forcefully close StateNew. The docs for Server.Close say
			// we wait for "outstanding requests", so we don't close things
			// in StateActive.
			if st == http.StateIdle || st == http.StateNew {
				s.closeConn(c)
			}
		}
		// If this server doesn't shut down in 5 seconds, tell the user why.
		t := time.AfterFunc(5*time.Second, s.logCloseHangDebugInfo)
		defer t.Stop()
	}
	s.mu.Unlock()

	// Not part of httptest.Server's correctness, but assume most
	// users of httptest.Server will be using the standard
	// transport, so help them out and close any idle connections for them.
	if t, ok := http.DefaultTransport.(closeIdleTransport); ok {
		t.CloseIdleConnections()
	}

	// Also close the client idle connections.
	if s.client != nil {
		if t, ok := s.client.Transport.(closeIdleTransport); ok {
			t.CloseIdleConnections()
		}
	}

	s.wg.Wait()
}

func (s *Server) logCloseHangDebugInfo() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var buf strings.Builder
	buf.WriteString("httptest.Server blocked in Close after 5 seconds, waiting for connections:\n")
	for c, st := range s.conns {
		fmt.Fprintf(&buf, "  %T %p %v in state %v\n", c, c, c.RemoteAddr(), st)
	}
	log.Print(buf.String())
}

type closeIdleTransport interface {
	CloseIdleConnections()
}

func main() {

	srv := newTestServer()
	NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.ServeHTTP(w, r)
	}))
	//defer httpsrv.Close()
	//defer srv.Stop()

	// server := newTestServer()
	// //defer server.Stop()

	// listener, err := net.Listen("tcp", "127.0.0.1:1234")
	// if err != nil {
	// 	log.Fatal("can't listen:", err)
	// }
	// log.Println(listener.Addr())
	// //defer listener.Close()
	// server.ServeListener(listener)

	// var (
	// 	request  = `{"jsonrpc":"2.0","id":1,"method":"rpc_modules"}` + "\n"
	// 	wantResp = `{"jsonrpc":"2.0","id":1,"result":{"nftest":"1.0","rpc":"1.0","test":"1.0"}}` + "\n"
	// 	deadline = time.Now().Add(10 * time.Second)
	// )

	// for i := 0; i < 20; i++ {
	// 	conn, err := net.Dial("tcp", listener.Addr().String())
	// 	if err != nil {
	// 		log.Fatal("can't dial:", err)
	// 	}

	// 	conn.SetDeadline(deadline)
	// 	// Write the request, then half-close the connection so the server stops reading.
	// 	conn.Write([]byte(request))
	// 	conn.(*net.TCPConn).CloseWrite()
	// 	// Now try to get the response.
	// 	buf := make([]byte, 2000)
	// 	n, err := conn.Read(buf)
	// 	conn.Close()

	// 	if err != nil {
	// 		log.Fatal("read error:", err)
	// 	}
	// 	//log.Println("LOL")
	// 	//log.Println(buf[:n])
	// 	if !bytes.Equal(buf[:n], []byte(wantResp)) {
	// 		log.Fatalf("wrong response: %s", buf[:n])
	// 	}
	// }
}
