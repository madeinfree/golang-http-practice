package main

import (
	"bufio"
	"context"
	"log"
	"net"
	"net/textproto"
	"strings"
	"sync"
)

// DefaultServeMux is the default ServeMux used by Serve.
var DefaultServeMux = &defaultServeMux
var defaultServeMux ServeMux

// A ServeMux defines
type ServeMux struct {
	mu    sync.RWMutex
	m     map[string]muxEntry
	hosts bool
}

// Handle implement into ServeMux
func (mux *ServeMux) Handle(pattern string, handler Handler) {
	mux.mu.Lock()
	defer mux.mu.Unlock()

	if pattern == "" {
		panic("http: invalid pattern")
	}

	if handler == nil {
		panic("http: nil handler")
	}

	if _, exist := mux.m[pattern]; exist {
		panic("http: multiple registrations for " + pattern)
	}

	if mux.m == nil {
		mux.m = make(map[string]muxEntry)
	}

	mux.m[pattern] = muxEntry{h: handler, pattern: pattern}

	if pattern[0] != '/' {
		mux.hosts = true
	}
}

//  muxEntry defines
type muxEntry struct {
	h       Handler
	pattern string
}

// A Handler interface.
type Handler interface {
	ServeHTTP(ResponseWriter, *Request)
}

// A HandlerFunc fun type
type HandlerFunc func(ResponseWriter, *Request)

// HandleFunc registers the handler for the given pattern
// http.HandleFunc("/users", userHandler)
func (mux *ServeMux) HandleFunc(pattern string, handler func(ResponseWriter, *Request)) {
	if handler == nil {
		panic("http: nil handler")
	}
	mux.Handle(pattern, HandlerFunc(handler))
}

// ServerHTTP rewrite HandlerFunc
func (handlerFunc HandlerFunc) ServeHTTP(write ResponseWriter, request *Request) {
	handlerFunc(write, request)
}

// A Request interface
type Request struct {
	Method     string // "GET","POST","PUT"...
	Proto      string // "HTTP/1.0"
	ProtoMajor int    // 1
	ProtoMinor int    // 0
}

// A ResponseWriter interface
type ResponseWriter interface {
	Write([]byte) (int, error)
	Flush()
	WriteHeader(statusCode int)
}

// A Server defines.
type Server struct {
	Addr    string
	Handler Handler
}

// A Conn defineds.
type conn struct {
	server     *Server
	rwc        net.Conn
	remoteAddr string
	mu         sync.Mutex
	bufr       *bufio.Reader
	bufw       *bufio.Writer
	r          *connReader
	w          *connWriter
}

// A ServerContext defineds.
type contextKey struct {
	name string
}

func (k *contextKey) String() string {
	return "net/http context value " + k.name
}

// A ServerContextKey var
var (
	ServerContextKey = &contextKey{"http-server"}
)

// Serve fun implement.
func (srv *Server) Serve(listen net.Listener) error {
	for {
		mconn, err := listen.Accept()
		if err != nil {
			return err
		}
		c := srv.newConn(mconn)
		baseCtx := context.Background()
		ctx := context.WithValue(baseCtx, ServerContextKey, srv)
		go c.serve(ctx)
	}
}

type serverHandler struct {
	srv *Server
}

type response struct {
	conn *conn
	req  *Request
	w    *bufio.Writer
}

func (w *response) Write(data []byte) (n int, err error) {
	return w.write(len(data), data, "")
}
func (w *response) write(lenData int, dataB []byte, dataS string) (n int, err error) {
	return w.w.WriteString(dataS)
}

func (sh serverHandler) ServeHTTP(response ResponseWriter, request *Request) {}

var textprotoReaderPool sync.Pool

func newTextprotoReader(br *bufio.Reader) *textproto.Reader {
	if v := textprotoReaderPool.Get(); v != nil {
		tr := v.(*textproto.Reader)
		tr.R = br
		return tr
	}
	return textproto.NewReader(br)
}

func readRequest(br *bufio.Reader, deleteHostHeader bool) (req *Request, err error) {
	tp := newTextprotoReader(br)
	req = new(Request)

	var s string
	if s, err = tp.ReadLine(); err != nil {
		return nil, err
	}
	s1 := strings.Index(s, " ")
	s2 := strings.Index(s[s1+1:], " ")
	s3 := strings.Split(s[s2+1:], "/")
	s2 += s1 + 1

	var method = s[:s1]
	var proto = strings.TrimLeft(s3[1], " ")

	req.Method = method
	req.Proto = proto

	return
}

type connReader struct {
	conn *conn
}

func (cr *connReader) Read(p []byte) (n int, err error) {
	n, err = cr.conn.rwc.Read(p)
	return n, err
}

type connWriter struct {
	conn *conn
}

func (cr *connWriter) Write(p []byte) (nn int, err error) {
	nn, err = cr.conn.bufw.Write(p)
	if err != nil {
		log.Fatal(err)
	}
	return
}

func (c *conn) readRequest(ctx context.Context) (w *response, err error) {
	c.r = &connReader{conn: c}
	c.bufr = bufio.NewReader(c.r)
	req, err := readRequest(c.bufr, false)

	w = &response{
		conn: c,
		req:  req,
	}

	return
}

func (c *conn) serve(ctx context.Context) {
	var httpVersion = "HTTP/1.1 "
	var httpStatus = "200"
	var firstHeadEnd = "\r\n"
	var contentType = "Content-Type: text/html; charset=utf-8"
	var setCookie = "Set-Cookie: myCookie=nice; path=/; HttpOnly;"
	var keepConnection = "Connection: close"
	var secondHeadersEnd = "\r\n"
	var allHeadersEnd = "\r\n\r\n"
	var htmlBuf = `<html>
<head>
  <style>body { background: black; }</style>
</head>
<body>
  <div>Hi</div>
</body>
</html>`

	c.remoteAddr = c.rwc.RemoteAddr().String()
	_, err := c.readRequest(ctx)
	if err != nil {
		log.Fatal(err)
	}
	c.w = &connWriter{conn: c}
	c.bufw = bufio.NewWriter(c.rwc)
	c.bufw.Write([]byte(
		httpVersion +
			httpStatus +
			firstHeadEnd +
			contentType +
			secondHeadersEnd +
			keepConnection +
			secondHeadersEnd +
			setCookie +
			allHeadersEnd +
			htmlBuf))
	c.bufw.Flush()
	c.rwc.Close()
}

func (srv *Server) newConn(mconn net.Conn) *conn {
	c := &conn{
		server: srv,
		rwc:    mconn,
	}
	return c
}

// ListenAndServe func implement.
func (srv *Server) ListenAndServe() error {
	addr := srv.Addr

	if addr == "" {
		addr = ":http"
	}

	listen, err := net.Listen("tcp", addr)

	if err != nil {
		log.Fatal(err)
	}

	if err := srv.Serve(listen); err != nil {
		log.Fatal(err)
	}

	return nil
}

// HandleFunc is given the pattern and match it.
func HandleFunc(pattern string, handler func(response ResponseWriter, request *Request)) {
	DefaultServeMux.HandleFunc(pattern, handler)
}

func main() {
	// HandleFunc(pattern, handler)
	server := &Server{Addr: ":3333", Handler: nil}
	server.ListenAndServe()
}
