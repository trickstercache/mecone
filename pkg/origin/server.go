/*
 * Copyright 2026 The Trickster Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package origin implements the Mecone origin role: a scriptable HTTP server that sits behind the
// proxy under test, answers each test's requests as scripted, and records what the proxy forwarded.
package origin

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	noAddr            = ""
	noTTL             = 0
	defaultScriptTTL  = 30 * time.Minute
	shutdownTimeout   = 5 * time.Second
	readHeaderTimeout = 10 * time.Second
	idleTimeout       = 2 * time.Minute
)

// Options configures a Server.
type Options struct {
	// Listen is the cleartext listen address, serving HTTP/1.1 and h2c; empty disables it.
	Listen string
	// TLSListen is the TLS listen address, serving HTTP/1.1 and HTTP/2; it requires TLSCert and TLSKey.
	TLSListen string
	TLSCert   string
	TLSKey    string
	// ScriptTTL bounds how long an abandoned test script is kept; zero means 30 minutes.
	ScriptTTL time.Duration
}

// Server is an origin that serves scripted test responses and the control plane.
type Server struct {
	opts   Options
	store  *store
	mux    *http.ServeMux
	plain  net.Listener
	secure net.Listener
}

type binding struct {
	srv   *http.Server
	serve func() error
}

// New returns a Server. Call Listen and then Serve, or mount Handler on a server of your own.
func New(o Options) *Server {
	if o.ScriptTTL <= noTTL {
		o.ScriptTTL = defaultScriptTTL
	}
	s := &Server{opts: o, store: newStore(o.ScriptTTL), mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the origin's HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.mux
}

// Listen binds the configured listeners without serving on them.
func (s *Server) Listen() error {
	if s.opts.Listen == noAddr && s.opts.TLSListen == noAddr {
		return errors.New("no listen address configured")
	}
	if err := s.checkKeyPair(); err != nil {
		return err
	}
	plain, err := bind(s.opts.Listen)
	if err != nil {
		return err
	}
	secure, err := bind(s.opts.TLSListen)
	if err != nil {
		return closeOnError(plain, err)
	}
	s.plain, s.secure = plain, secure
	return nil
}

func (s *Server) checkKeyPair() error {
	if s.opts.TLSListen == noAddr {
		return nil
	}
	if _, err := tls.LoadX509KeyPair(s.opts.TLSCert, s.opts.TLSKey); err != nil {
		return fmt.Errorf("loading TLS key pair: %w", err)
	}
	return nil
}

func bind(addr string) (net.Listener, error) {
	if addr == noAddr {
		return nil, nil
	}
	return net.Listen("tcp", addr)
}

func closeOnError(ln net.Listener, err error) error {
	if ln == nil {
		return err
	}
	return errors.Join(err, ln.Close())
}

// Addr returns the bound cleartext address, or "" when that listener is not bound.
func (s *Server) Addr() string {
	if s.plain == nil {
		return noAddr
	}
	return s.plain.Addr().String()
}

// TLSAddr returns the bound TLS address, or "" when that listener is not bound.
func (s *Server) TLSAddr() string {
	if s.secure == nil {
		return noAddr
	}
	return s.secure.Addr().String()
}

// Close releases the listeners bound by Listen; use it instead of Serve when the server will not run.
func (s *Server) Close() error {
	err := errors.Join(closeListener(s.plain), closeListener(s.secure))
	s.plain, s.secure = nil, nil
	return err
}

func closeListener(ln net.Listener) error {
	if ln == nil {
		return nil
	}
	return ln.Close()
}

// Serve serves on the bound listeners until ctx is done, then shuts down gracefully.
func (s *Server) Serve(ctx context.Context) error {
	if s.plain == nil && s.secure == nil {
		return errors.New("no listeners are bound; call Listen first")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	bindings := s.bindings()
	var wg sync.WaitGroup
	for _, b := range bindings {
		wg.Go(func() { b.run(cancel) })
	}
	<-ctx.Done()
	shutdown(ctx, bindings)
	wg.Wait()
	return serveError(ctx)
}

func (s *Server) bindings() []binding {
	var out []binding
	if ln := s.plain; ln != nil {
		srv := s.httpServer(cleartextProtocols())
		out = append(out, binding{srv: srv, serve: func() error { return srv.Serve(ln) }})
	}
	if ln := s.secure; ln != nil {
		srv := s.httpServer(tlsProtocols())
		out = append(out, binding{srv: srv, serve: func() error { return srv.ServeTLS(ln, s.opts.TLSCert, s.opts.TLSKey) }})
	}
	return out
}

func (b binding) run(cancel context.CancelCauseFunc) {
	if err := b.serve(); !errors.Is(err, http.ErrServerClosed) {
		cancel(err)
	}
}

func shutdown(ctx context.Context, bindings []binding) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	for _, b := range bindings {
		_ = b.srv.Shutdown(sctx)
	}
}

func serveError(ctx context.Context) error {
	if err := context.Cause(ctx); !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

func (s *Server) httpServer(p *http.Protocols) *http.Server {
	return &http.Server{
		Handler:           s.mux,
		Protocols:         p,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func cleartextProtocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetUnencryptedHTTP2(true)
	return p
}

func tlsProtocols() *http.Protocols {
	p := new(http.Protocols)
	p.SetHTTP1(true)
	p.SetHTTP2(true)
	return p
}

func (s *Server) listeners() []string {
	var out []string
	if a := s.Addr(); a != noAddr {
		out = append(out, "http://"+a+" (h1, h2c)")
	}
	if a := s.TLSAddr(); a != noAddr {
		out = append(out, "https://"+a+" (h1, h2)")
	}
	return out
}
