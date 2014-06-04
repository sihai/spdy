// Copyright 2013 Jamie Hall. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package spdy2

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/SlyMarbo/spdy/common"
	"github.com/SlyMarbo/spdy/spdy2/frames"
	"github.com/SlyMarbo/spin"
)

// Conn is a spdy.Conn implementing SPDY/2. This is used in both
// servers and clients, and is created with either NewServerConn,
// or NewClientConn.
type Conn struct {
	sync.Mutex
	PushReceiver common.Receiver // Receiver to call for server Pushes.

	remoteAddr            string
	server                *http.Server
	conn                  net.Conn
	connLock              spin.Lock // protects the interface value of the above conn.
	buf                   *bufio.Reader
	tlsState              *tls.ConnectionState
	streams               map[common.StreamID]common.Stream     // map of active streams.
	output                [8]chan common.Frame                  // one output channel per priority level.
	pings                 map[uint32]chan<- common.Ping         // response channel for pings.
	nextPingID            uint32                                // next outbound ping ID.
	compressor            common.Compressor                     // outbound compression state.
	decompressor          common.Decompressor                   // inbound decompression state.
	receivedSettings      common.Settings                       // settings sent by client.
	lastPushStreamID      common.StreamID                       // last push stream ID. (even)
	lastRequestStreamID   common.StreamID                       // last request stream ID. (odd)
	oddity                common.StreamID                       // whether locally-sent streams are odd or even.
	initialWindowSize     uint32                                // initial transport window.
	initialWindowSizeLock spin.Lock                             // lock for initialWindowSize
	goawayReceived        bool                                  // goaway has been received.
	goawaySent            bool                                  // goaway has been sent.
	numBenignErrors       int                                   // number of non-serious errors encountered.
	requestStreamLimit    *common.StreamLimit                   // Limit on streams started by the client.
	pushStreamLimit       *common.StreamLimit                   // Limit on streams started by the server.
	pushRequests          map[common.StreamID]*http.Request     // map of requests sent in server pushes.
	stop                  chan bool                             // this channel is closed when the connection closes.
	sending               chan struct{}                         // this channel is used to ensure pending frames are sent.
	sendingLock           spin.Lock                             // protects changes to sending's value.
	init                  func()                                // this function is called before the connection begins.
	readTimeout           time.Duration                         // optional timeout for network reads.
	writeTimeout          time.Duration                         // optional timeout for network writes.
	pushedResources       map[common.Stream]map[string]struct{} // used to prevent duplicate headers being pushed.
	shutdownOnce          sync.Once                             // used to ensure clean shutdown.
}

// NewConn produces an initialised spdy3 connection.
func NewConn(conn net.Conn, server *http.Server) *Conn {
	out := new(Conn)

	// Common ground.
	out.remoteAddr = conn.RemoteAddr().String()
	out.server = server
	out.conn = conn
	out.buf = bufio.NewReader(conn)
	if tlsConn, ok := conn.(*tls.Conn); ok {
		out.tlsState = new(tls.ConnectionState)
		*out.tlsState = tlsConn.ConnectionState()
	}
	out.streams = make(map[common.StreamID]common.Stream)
	out.output[0] = make(chan common.Frame)
	out.output[1] = make(chan common.Frame)
	out.output[2] = make(chan common.Frame)
	out.output[3] = make(chan common.Frame)
	out.output[4] = make(chan common.Frame)
	out.output[5] = make(chan common.Frame)
	out.output[6] = make(chan common.Frame)
	out.output[7] = make(chan common.Frame)
	out.pings = make(map[uint32]chan<- common.Ping)
	out.compressor = common.NewCompressor(2)
	out.decompressor = common.NewDecompressor(2)
	out.receivedSettings = make(common.Settings)
	out.lastPushStreamID = 0
	out.lastRequestStreamID = 0
	out.stop = make(chan bool)

	// Server/client specific.
	if server != nil { // servers
		out.nextPingID = 2
		out.oddity = 0
		out.initialWindowSize = common.DEFAULT_INITIAL_WINDOW_SIZE
		out.requestStreamLimit = common.NewStreamLimit(common.DEFAULT_STREAM_LIMIT)
		out.pushStreamLimit = common.NewStreamLimit(common.NO_STREAM_LIMIT)
		out.init = func() {
			// Initialise the connection by sending the connection settings.
			settings := new(frames.SETTINGS)
			settings.Settings = defaultServerSettings(common.DEFAULT_STREAM_LIMIT)
			out.output[0] <- settings
		}
		if d := server.ReadTimeout; d != 0 {
			out.SetReadTimeout(d)
		}
		if d := server.WriteTimeout; d != 0 {
			out.SetWriteTimeout(d)
		}
		out.pushedResources = make(map[common.Stream]map[string]struct{})

	} else { // clients
		out.nextPingID = 1
		out.oddity = 1
		out.initialWindowSize = common.DEFAULT_INITIAL_CLIENT_WINDOW_SIZE
		out.requestStreamLimit = common.NewStreamLimit(common.NO_STREAM_LIMIT)
		out.pushStreamLimit = common.NewStreamLimit(common.DEFAULT_STREAM_LIMIT)
		out.pushRequests = make(map[common.StreamID]*http.Request)
		out.init = func() {
			// Initialise the connection by sending the connection settings.
			settings := new(frames.SETTINGS)
			settings.Settings = defaultClientSettings(common.DEFAULT_STREAM_LIMIT)
			out.output[0] <- settings
		}
	}
	return out
}

// Close ends the connection, cleaning up relevant resources.
// Close can be called multiple times safely.
func (conn *Conn) Close() (err error) {
	conn.shutdownOnce.Do(conn.shutdown)
	return nil
}

func (conn *Conn) shutdown() {
	if conn.closed() {
		return
	}

	// Try to inform the other endpoint that the connection is closing.
	conn.sendingLock.Lock()
	isSending := conn.sending != nil
	conn.sendingLock.Unlock()
	if !conn.goawaySent && !isSending {
		goaway := new(frames.GOAWAY)
		if conn.server != nil {
			goaway.LastGoodStreamID = conn.lastRequestStreamID
		} else {
			goaway.LastGoodStreamID = conn.lastPushStreamID
		}
		select {
		case conn.output[0] <- goaway:
			conn.goawaySent = true
		case <-time.After(100 * time.Millisecond):
			debug.Println("Failed to send closing GOAWAY.")
		}
	}

	// Give any pending frames 200ms to send.
	conn.sendingLock.Lock()
	if conn.sending == nil {
		conn.sending = make(chan struct{})
		conn.sendingLock.Unlock()
		select {
		case <-conn.sending:
		case <-time.After(200 * time.Millisecond):
		}
		conn.sendingLock.Lock()
	}
	conn.sending = nil
	conn.sendingLock.Unlock()

	select {
	case _, ok := <-conn.stop:
		if ok {
			close(conn.stop)
		}
	default:
		close(conn.stop)
	}

	conn.connLock.Lock()
	if conn.conn != nil {
		conn.conn.Close()
		conn.conn = nil
	}
	conn.connLock.Unlock()

	for _, stream := range conn.streams {
		if err := stream.Close(); err != nil {
			debug.Println(err)
		}
	}
	conn.streams = nil

	if conn.compressor != nil {
		conn.compressor.Close()
		conn.compressor = nil
	}
	conn.decompressor = nil

	conn.pushedResources = nil

	for _, stream := range conn.output {
		select {
		case _, ok := <-stream:
			if ok {
				close(stream)
			}
		default:
			close(stream)
		}
	}
}

func (conn *Conn) CloseNotify() <-chan bool {
	return conn.stop
}

func (conn *Conn) Conn() net.Conn {
	conn.connLock.Lock()
	c := conn.conn
	conn.connLock.Unlock()
	return c
}

// InitialWindowSize gives the most recently-received value for
// the INITIAL_WINDOW_SIZE setting.
func (conn *Conn) InitialWindowSize() (uint32, error) {
	conn.initialWindowSizeLock.Lock()
	i := conn.initialWindowSize
	conn.initialWindowSizeLock.Unlock()
	return i, nil
}

// Ping is used by spdy.PingServer and spdy.PingClient to send
// SPDY PINGs.
func (conn *Conn) Ping() (<-chan common.Ping, error) {
	conn.Lock()
	defer conn.Unlock()

	if conn.closed() {
		return nil, errors.New("Error: Conn has been closed.")
	}

	ping := new(frames.PING)
	pid := conn.nextPingID
	if pid+2 < pid {
		if pid&1 == 0 {
			conn.nextPingID = 2
		} else {
			conn.nextPingID = 1
		}
	} else {
		conn.nextPingID += 2
	}
	ping.PingID = pid
	conn.output[0] <- ping
	c := make(chan common.Ping, 1)
	conn.pings[pid] = c

	return c, nil
}

// Push is used to issue a server push to the client. Note that this cannot be performed
// by clients.
func (conn *Conn) Push(resource string, origin common.Stream) (common.PushStream, error) {
	if conn.goawayReceived || conn.goawaySent {
		return nil, common.ErrGoaway
	}

	if conn.server == nil {
		return nil, errors.New("Error: Only servers can send pushes.")
	}

	// Parse and check URL.
	url, err := url.Parse(resource)
	if err != nil {
		return nil, err
	}
	if url.Scheme == "" || url.Host == "" {
		return nil, errors.New("Error: Incomplete path provided to resource.")
	}
	resource = url.String()

	// Ensure the resource hasn't been pushed on the given stream already.
	if conn.pushedResources[origin] == nil {
		conn.pushedResources[origin] = map[string]struct{}{
			resource: struct{}{},
		}
	} else if _, ok := conn.pushedResources[origin][url.String()]; !ok {
		conn.pushedResources[origin][resource] = struct{}{}
	} else {
		return nil, errors.New("Error: Resource already pushed to this stream.")
	}

	// Check stream limit would allow the new stream.
	if !conn.pushStreamLimit.Add() {
		return nil, errors.New("Error: Max concurrent streams limit exceeded.")
	}

	// Verify that path is prefixed with / as required by spec.
	path := url.Path
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	// Prepare the SYN_STREAM.
	push := new(frames.SYN_STREAM)
	push.Flags = common.FLAG_UNIDIRECTIONAL
	push.AssocStreamID = origin.StreamID()
	push.Priority = 3
	push.Header = make(http.Header)
	push.Header.Set("scheme", url.Scheme)
	push.Header.Set("host", url.Host)
	push.Header.Set("url", path)
	push.Header.Set("version", "HTTP/1.1")
	push.Header.Set(":status", "200 OK")

	// Send.
	conn.Lock()
	defer conn.Unlock()

	conn.lastPushStreamID += 2
	if conn.lastPushStreamID > common.MAX_STREAM_ID {
		return nil, errors.New("Error: All server streams exhausted.")
	}
	newID := conn.lastPushStreamID
	push.StreamID = newID
	conn.output[0] <- push

	// Create the PushStream.
	out := new(PushStream)
	out.conn = conn
	out.streamID = newID
	out.origin = origin
	out.state = new(common.StreamState)
	out.output = conn.output[3]
	out.header = make(http.Header)
	out.stop = conn.stop

	// Store in the connection map.
	conn.streams[newID] = out

	return out, nil
}

// Request is used to make a client request.
func (conn *Conn) Request(request *http.Request, receiver common.Receiver, priority common.Priority) (common.Stream, error) {
	if conn.goawayReceived || conn.goawaySent {
		return nil, common.ErrGoaway
	}

	if conn.server != nil {
		return nil, errors.New("Error: Only clients can send requests.")
	}

	// Check stream limit would allow the new stream.
	if !conn.requestStreamLimit.Add() {
		return nil, errors.New("Error: Max concurrent streams limit exceeded.")
	}

	if !priority.Valid(2) {
		return nil, errors.New("Error: Priority must be in the range 0 - 7.")
	}

	url := request.URL
	if url == nil || url.Scheme == "" || url.Host == "" {
		return nil, errors.New("Error: Incomplete path provided to resource.")
	}

	// Prepare the SYN_STREAM.
	path := url.Path
	if url.RawQuery != "" {
		path += "?" + url.RawQuery
	}
	if url.Fragment != "" {
		path += "#" + url.Fragment
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	syn := new(frames.SYN_STREAM)
	syn.Priority = priority
	syn.Header = request.Header
	syn.Header.Set("method", request.Method)
	syn.Header.Set("url", path)
	syn.Header.Set("version", "HTTP/1.1")
	syn.Header.Set("host", url.Host)
	syn.Header.Set("scheme", url.Scheme)

	// Prepare the request body, if any.
	body := make([]*frames.DATA, 0, 1)
	if request.Body != nil {
		buf := make([]byte, 32*1024)
		n, err := request.Body.Read(buf)
		if err != nil && err != io.EOF {
			return nil, err
		}
		total := n
		for n > 0 {
			data := new(frames.DATA)
			data.Data = make([]byte, n)
			copy(data.Data, buf[:n])
			body = append(body, data)
			n, err = request.Body.Read(buf)
			if err != nil && err != io.EOF {
				return nil, err
			}
			total += n
		}

		// Half-close the stream.
		if len(body) == 0 {
			syn.Flags = common.FLAG_FIN
		} else {
			syn.Header.Set("Content-Length", fmt.Sprint(total))
			body[len(body)-1].Flags = common.FLAG_FIN
		}
		request.Body.Close()
	} else {
		syn.Flags = common.FLAG_FIN
	}

	// Send.
	conn.Lock()
	defer conn.Unlock()

	if conn.lastRequestStreamID == 0 {
		conn.lastRequestStreamID = 1
	} else {
		conn.lastRequestStreamID += 2
	}
	if conn.lastRequestStreamID > common.MAX_STREAM_ID {
		return nil, errors.New("Error: All client streams exhausted.")
	}
	syn.StreamID = conn.lastRequestStreamID
	conn.output[0] <- syn
	for _, frame := range body {
		frame.StreamID = syn.StreamID
		conn.output[0] <- frame
	}

	// // Create the request stream.
	out := new(ClientStream)
	out.conn = conn
	out.streamID = syn.StreamID
	out.state = new(common.StreamState)
	out.state.CloseHere()
	out.output = conn.output[0]
	out.request = request
	out.receiver = receiver
	out.header = make(http.Header)
	out.stop = conn.stop
	out.finished = make(chan struct{})
	out.headerChan = make(chan func(), 5)
	go out.processFrames()

	// Store in the connection map.
	conn.streams[syn.StreamID] = out

	return out, nil
}

func (conn *Conn) RequestResponse(request *http.Request, receiver common.Receiver, priority common.Priority) (*http.Response, error) {
	res := new(common.Response)
	res.Request = request
	res.Data = new(bytes.Buffer)
	res.Receiver = receiver

	// Send the request.
	stream, err := conn.Request(request, res, priority)
	if err != nil {
		return nil, err
	}

	// Let the request run its course.
	stream.Run()

	return res.Response(), nil
}

func (conn *Conn) Run() error {
	// Start the send loop.
	go conn.send()

	// Prepare any initialisation frames.
	if conn.init != nil {
		conn.init()
	}

	// Start the main loop.
	go conn.readFrames()

	// Run until the connection ends.
	<-conn.stop

	return nil
}

func (conn *Conn) SetFlowControl(common.FlowControl) error {
	return common.ErrNoFlowControl
}

func (conn *Conn) SetTimeout(d time.Duration) {
	conn.Lock()
	conn.readTimeout = d
	conn.writeTimeout = d
	conn.Unlock()
}

func (conn *Conn) SetReadTimeout(d time.Duration) {
	conn.Lock()
	conn.readTimeout = d
	conn.Unlock()
}

func (conn *Conn) SetWriteTimeout(d time.Duration) {
	conn.Lock()
	conn.writeTimeout = d
	conn.Unlock()
}

// closed indicates whether the connection has
// been closed.
func (conn *Conn) closed() bool {
	select {
	case _ = <-conn.stop:
		return true
	default:
		return false
	}
}

// handleClientData performs the processing of DATA frames sent by the client.
func (conn *Conn) handleClientData(frame *frames.DATA) {
	conn.Lock()

	sid := frame.StreamID

	if conn.server == nil {
		log.Println("Error: Requests can only be received by the server.")
		conn.numBenignErrors++
		conn.Unlock()
		return
	}

	// Handle push data.
	if sid&1 == 0 {
		log.Printf("Error: Received DATA with Stream ID %d, which should be odd.\n", sid)
		conn.numBenignErrors++
		conn.Unlock()
		return
	}

	// Check stream ID is valid.
	if !sid.Valid() {
		log.Printf("Error: Received DATA with Stream ID %d, which exceeds the limit.\n", sid)
		conn.Unlock()
		conn.protocolError(sid)
		return
	}

	// Check stream is open.
	stream, ok := conn.streams[sid]
	if !ok || stream == nil || stream.State().ClosedThere() {
		if ok {
			debug.Printf("Warning: Received DATA with Stream ID %d, which is closed.\n", sid)
		} else {
			debug.Printf("Error: Received DATA with Stream ID %d, which is unopened.\n", sid)
			conn.numBenignErrors++
		}
		conn.Unlock()
		return
	}
	conn.Unlock()

	// Stream ID is fine.

	// Send data to stream.
	stream.ReceiveFrame(frame)
}

// handleHeaders performs the processing of HEADERS frames.
func (conn *Conn) handleHeaders(frame *frames.HEADERS) {
	conn.Lock()

	sid := frame.StreamID

	// Handle push headers.
	if sid&1 == 0 && conn.server == nil {
		// Ignore refused push headers.
		conn.Unlock()
		if req := conn.pushRequests[sid]; req != nil && conn.PushReceiver != nil {
			conn.PushReceiver.ReceiveHeader(req, frame.Header)
		}
		return
	}

	// Check stream is open.
	stream, ok := conn.streams[sid]
	if !ok || stream == nil || stream.State().ClosedThere() {
		if ok {
			debug.Printf("Warning: Received HEADERS with Stream ID %d, which is closed.\n", sid)
		} else {
			debug.Printf("Error: Received HEADERS with Stream ID %d, which is unopened.\n", sid)
			conn.numBenignErrors++
		}
		conn.Unlock()
		return
	}
	conn.Unlock()

	// Stream ID is fine.

	// Send headers to stream.
	stream.ReceiveFrame(frame)
}

// handlePush performs the processing of SYN_STREAM frames forming a server push.
func (conn *Conn) handlePush(frame *frames.SYN_STREAM) {
	conn.Lock()

	// Check stream creation is allowed.
	if conn.goawayReceived || conn.goawaySent || conn.closed() {
		conn.Unlock()
		return
	}

	sid := frame.StreamID

	// Push.
	if conn.server != nil {
		log.Println("Error: Only clients can receive server pushes.")
		conn.Unlock()
		return
	}

	// Check Stream ID is even.
	if sid&1 != 0 {
		log.Printf("Error: Received SYN_STREAM with Stream ID %d, which should be even.\n", sid)
		conn.numBenignErrors++
		conn.Unlock()
		return
	}

	// Check Stream ID is the right number.
	lsid := conn.lastPushStreamID
	if sid <= lsid {
		log.Printf("Error: Received SYN_STREAM with Stream ID %d, which should be greater than %d.\n", sid, lsid)
		conn.numBenignErrors++
		conn.Unlock()
		return
	}

	// Check Stream ID is not out of bounds.
	if !sid.Valid() {
		log.Printf("Error: Received SYN_STREAM with Stream ID %d, which exceeds the limit.\n", sid)
		conn.Unlock()
		conn.protocolError(sid)
		return
	}

	// Stream ID is fine.

	// Check stream limit would allow the new stream.
	if !conn.pushStreamLimit.Add() {
		rst := new(frames.RST_STREAM)
		rst.StreamID = sid
		rst.Status = common.RST_STREAM_REFUSED_STREAM
		conn.output[0] <- rst
		conn.Unlock()
		return
	}

	if !frame.Priority.Valid(2) {
		log.Printf("Error: Received SYN_STREAM with invalid priority %d.\n", frame.Priority)
		conn.Unlock()
		conn.protocolError(sid)
		return
	}

	// Parse the request.
	header := frame.Header
	rawUrl := header.Get("scheme") + "://" + header.Get("host") + header.Get("url")
	url, err := url.Parse(rawUrl)
	if err != nil {
		log.Println("Error: Received SYN_STREAM with invalid request URL: ", err)
		conn.Unlock()
		return
	}

	vers := header.Get("version")
	major, minor, ok := http.ParseHTTPVersion(vers)
	if !ok {
		log.Println("Error: Invalid HTTP version: " + vers)
		conn.Unlock()
		return
	}

	method := header.Get("method")

	request := &http.Request{
		Method:     method,
		URL:        url,
		Proto:      vers,
		ProtoMajor: major,
		ProtoMinor: minor,
		RemoteAddr: conn.remoteAddr,
		Header:     header,
		Host:       url.Host,
		RequestURI: url.RequestURI(),
		TLS:        conn.tlsState,
	}

	// Check whether the receiver wants this resource.
	if conn.PushReceiver != nil && !conn.PushReceiver.ReceiveRequest(request) {
		rst := new(frames.RST_STREAM)
		rst.StreamID = sid
		rst.Status = common.RST_STREAM_REFUSED_STREAM
		conn.output[0] <- rst
		conn.Unlock()
		return
	}

	// Create and start new stream.
	if conn.PushReceiver != nil {
		conn.pushRequests[sid] = request
		conn.lastPushStreamID = sid
		conn.Unlock()
		conn.PushReceiver.ReceiveHeader(request, frame.Header)
	} else {
		conn.Unlock()
	}
}

// handleRequest performs the processing of SYN_STREAM request frames.
func (conn *Conn) handleRequest(frame *frames.SYN_STREAM) {
	conn.Lock()
	defer conn.Unlock()

	// Check stream creation is allowed.
	if conn.goawayReceived || conn.goawaySent || conn.closed() {
		return
	}

	sid := frame.StreamID

	if conn.server == nil {
		log.Println("Error: Only servers can receive requests.")
		return
	}

	// Check Stream ID is odd.
	if sid&1 == 0 {
		log.Printf("Error: Received SYN_STREAM with Stream ID %d, which should be odd.\n", sid)
		conn.numBenignErrors++
		return
	}

	// Check Stream ID is the right number.
	lsid := conn.lastRequestStreamID
	if sid <= lsid && lsid != 0 {
		log.Printf("Error: Received SYN_STREAM with Stream ID %d, which should be greater than %d.\n", sid, lsid)
		conn.numBenignErrors++
		return
	}

	// Check Stream ID is not out of bounds.
	if !sid.Valid() {
		log.Printf("Error: Received SYN_STREAM with Stream ID %d, which exceeds the limit.\n", sid)
		conn.Unlock()
		conn.protocolError(sid)
		conn.Lock()
		return
	}

	// Stream ID is fine.

	// Check stream limit would allow the new stream.
	if !conn.requestStreamLimit.Add() {
		rst := new(frames.RST_STREAM)
		rst.StreamID = sid
		rst.Status = common.RST_STREAM_REFUSED_STREAM
		conn.output[0] <- rst
		return
	}

	// Check request priority.
	if !frame.Priority.Valid(2) {
		log.Printf("Error: Received SYN_STREAM with invalid priority %d.\n", frame.Priority)
		conn.Unlock()
		conn.protocolError(sid)
		conn.Lock()
		return
	}

	// Create and start new stream.
	nextStream := conn.newStream(frame, frame.Priority)
	// Make sure an error didn't occur when making the stream.
	if nextStream == nil {
		return
	}

	// Set and prepare.
	conn.streams[sid] = nextStream
	conn.lastRequestStreamID = sid

	// Start the stream.
	go nextStream.Run()
}

// handleRstStream performs the processing of RST_STREAM frames.
func (conn *Conn) handleRstStream(frame *frames.RST_STREAM) {
	conn.Lock()
	defer conn.Unlock()

	sid := frame.StreamID

	// Determine the status code and react accordingly.
	switch frame.Status {
	case common.RST_STREAM_INVALID_STREAM:
		log.Printf("Error: Received INVALID_STREAM for stream ID %d.\n", sid)
		if stream, ok := conn.streams[sid]; ok {
			go stream.Close()
		}
		conn.numBenignErrors++

	case common.RST_STREAM_REFUSED_STREAM:
		if stream, ok := conn.streams[sid]; ok {
			go stream.Close()
		}

	case common.RST_STREAM_CANCEL:
		if sid&1 == conn.oddity {
			log.Println("Error: Cannot cancel locally-sent streams.")
			conn.numBenignErrors++
			return
		}
		if stream, ok := conn.streams[sid]; ok {
			go stream.Close()
		}

	case common.RST_STREAM_FLOW_CONTROL_ERROR:
		log.Printf("Error: Received FLOW_CONTROL_ERROR for stream ID %d.\n", sid)
		conn.numBenignErrors++
		return

	case common.RST_STREAM_STREAM_IN_USE:
		log.Printf("Error: Received STREAM_IN_USE for stream ID %d.\n", sid)
		conn.numBenignErrors++

	case common.RST_STREAM_STREAM_ALREADY_CLOSED:
		log.Printf("Error: Received STREAM_ALREADY_CLOSED for stream ID %d.\n", sid)
		if stream, ok := conn.streams[sid]; ok {
			go stream.Close()
		}
		conn.numBenignErrors++

	case common.RST_STREAM_INVALID_CREDENTIALS:
		log.Printf("Error: Received INVALID_CREDENTIALS for stream ID %d.\n", sid)
		conn.numBenignErrors++

	default:
		log.Printf("Error: Received unknown RST_STREAM status code %d.\n", frame.Status)
		conn.Unlock()
		conn.protocolError(sid)
		conn.Lock()
		return
	}
}

// handleServerData performs the processing of DATA frames sent by the server.
func (conn *Conn) handleServerData(frame *frames.DATA) {
	conn.Lock()

	sid := frame.StreamID

	// Handle push data.
	if sid&1 == 0 {
		// Ignore refused push data.
		conn.Unlock()
		if req := conn.pushRequests[sid]; req != nil && conn.PushReceiver != nil {
			conn.PushReceiver.ReceiveData(req, frame.Data, frame.Flags.FIN())
		}
		return
	}

	// Check stream is open.
	stream, ok := conn.streams[sid]
	if !ok || stream == nil || stream.State().ClosedThere() {
		if ok {
			debug.Printf("Warning: Received DATA with Stream ID %d, which is closed.\n", sid)
		} else {
			debug.Printf("Error: Received DATA with Stream ID %d, which is unopened.\n", sid)
			conn.numBenignErrors++
		}
		conn.Unlock()
		return
	}
	conn.Unlock()

	// Stream ID is fine.

	// Send data to stream.
	stream.ReceiveFrame(frame)
}

// handleSynReply performs the processing of SYN_REPLY frames.
func (conn *Conn) handleSynReply(frame *frames.SYN_REPLY) {
	conn.Lock()

	sid := frame.StreamID

	if conn.server != nil {
		log.Println("Error: Only clients can receive SYN_REPLY frames.")
		conn.numBenignErrors++
		conn.Unlock()
		return
	}

	// Check Stream ID is odd.
	if sid&1 == 0 {
		log.Printf("Error: Received SYN_REPLY with Stream ID %d, which should be odd.\n", sid)
		conn.numBenignErrors++
		conn.Unlock()
		return
	}

	if !sid.Valid() {
		log.Printf("Error: Received SYN_REPLY with Stream ID %d, which exceeds the limit.\n", sid)
		conn.Unlock()
		conn.protocolError(sid)
		return
	}

	// Check stream is open.
	if stream, ok := conn.streams[sid]; !ok || stream == nil || stream.State().ClosedThere() {
		log.Printf("Error: Received SYN_REPLY with Stream ID %d, which is closed or unopened.\n", sid)
		conn.numBenignErrors++
		conn.Unlock()
		return
	}
	conn.Unlock()

	// Stream ID is fine.

	// Send headers to stream.
	conn.streams[sid].ReceiveFrame(frame)
}

// newStream is used to create a new serverStream from a SYN_STREAM frame.
func (conn *Conn) newStream(frame *frames.SYN_STREAM, priority common.Priority) *ServerStream {
	stream := new(ServerStream)
	stream.conn = conn
	stream.streamID = frame.StreamID
	stream.requestBody = new(bytes.Buffer)
	stream.state = new(common.StreamState)
	stream.output = conn.output[priority]
	// stream.request initialised below
	stream.handler = conn.server.Handler
	if stream.handler == nil {
		stream.handler = http.DefaultServeMux
	}
	stream.header = make(http.Header)
	stream.unidirectional = frame.Flags.UNIDIRECTIONAL()
	stream.responseCode = 0
	stream.ready = make(chan struct{})
	stream.stop = conn.stop
	stream.wroteHeader = false
	stream.Priority = priority

	if frame.Flags.FIN() {
		close(stream.ready)
		stream.state.CloseThere()
	}

	header := frame.Header
	rawUrl := header.Get("scheme") + "://" + header.Get("host") + header.Get("url")

	url, err := url.Parse(rawUrl)
	if err != nil {
		log.Println("Error: Received SYN_STREAM with invalid request URL: ", err)
		return nil
	}

	vers := header.Get("version")
	major, minor, ok := http.ParseHTTPVersion(vers)
	if !ok {
		log.Println("Error: Invalid HTTP version: " + vers)
		return nil
	}

	method := header.Get("method")

	// Build this into a request to present to the Handler.
	stream.request = &http.Request{
		Method:     method,
		URL:        url,
		Proto:      vers,
		ProtoMajor: major,
		ProtoMinor: minor,
		RemoteAddr: conn.remoteAddr,
		Header:     header,
		Host:       url.Host,
		RequestURI: url.RequestURI(),
		TLS:        conn.tlsState,
		Body:       &common.ReadCloser{stream.requestBody},
	}

	return stream
}

// handleReadWriteError differentiates between normal and
// unexpected errors when performing I/O with the network,
// then shuts down the connection.
func (conn *Conn) handleReadWriteError(err error) {
	if _, ok := err.(*net.OpError); ok || err == io.EOF || err == common.ErrConnNil {
		// Server has closed the TCP connection.
		debug.Println("Note: Endpoint has disconnected.")
	} else {
		// Unexpected error which prevented a read/write.
		log.Printf("Error: Encountered error: %q (%T)\n", err.Error(), err)
	}

	// Make sure conn.Close succeeds and sending stops.
	conn.sendingLock.Lock()
	if conn.sending == nil {
		conn.sending = make(chan struct{})
	}
	conn.sendingLock.Unlock()

	conn.Close()
}

// protocolError informs the other endpoint that a protocol error has
// occurred, stops all running streams, and ends the connection.
func (conn *Conn) protocolError(streamID common.StreamID) {
	reply := new(frames.RST_STREAM)
	reply.StreamID = streamID
	reply.Status = common.RST_STREAM_PROTOCOL_ERROR
	select {
	case conn.output[0] <- reply:
	case <-time.After(100 * time.Millisecond):
		debug.Println("Failed to send PROTOCOL_ERROR RST_STREAM.")
		conn.Close()
		return
	}

	if !conn.goawaySent {
		goaway := new(frames.GOAWAY)
		if conn.server != nil {
			goaway.LastGoodStreamID = conn.lastRequestStreamID
		} else {
			goaway.LastGoodStreamID = conn.lastPushStreamID
		}
		select {
		case conn.output[0] <- goaway:
			conn.goawaySent = true
		case <-time.After(100 * time.Millisecond):
			debug.Println("Failed to send PROTOCOL_ERROR GOAWAY.")
		}
	}

	conn.Close()
}

// processFrame handles the initial processing of the given
// frame, before passing it on to the relevant helper func,
// if necessary. The returned boolean indicates whether the
// connection is closing.
func (conn *Conn) processFrame(frame common.Frame) bool {
	switch frame := frame.(type) {

	case *frames.SYN_STREAM:
		if conn.server == nil {
			conn.handlePush(frame)
		} else {
			conn.handleRequest(frame)
		}

	case *frames.SYN_REPLY:
		conn.handleSynReply(frame)

	case *frames.RST_STREAM:
		if frame.Status.IsFatal() {
			code := frame.Status.String()
			log.Printf("Warning: Received %s on stream %d. Closing connection.\n", code, frame.StreamID)
			conn.Close()
			return true
		}
		conn.handleRstStream(frame)

	case *frames.SETTINGS:
		for _, setting := range frame.Settings {
			conn.receivedSettings[setting.ID] = setting
			switch setting.ID {
			case common.SETTINGS_INITIAL_WINDOW_SIZE:
				conn.initialWindowSizeLock.Lock()
				conn.initialWindowSize = setting.Value
				conn.initialWindowSizeLock.Unlock()

			case common.SETTINGS_MAX_CONCURRENT_STREAMS:
				if conn.server == nil {
					conn.requestStreamLimit.SetLimit(setting.Value)
				} else {
					conn.pushStreamLimit.SetLimit(setting.Value)
				}
			}
		}

	case *frames.NOOP:
		// Ignore.

	case *frames.PING:
		// Check whether Ping ID is a response.
		if frame.PingID&1 == conn.nextPingID&1 {
			conn.Lock()
			if conn.pings[frame.PingID] == nil {
				log.Printf("Warning: Ignored PING with Ping ID %d, which hasn't been requested.\n",
					frame.PingID)
				conn.numBenignErrors++
				conn.Unlock()
				return false
			}
			conn.pings[frame.PingID] <- common.Ping{}
			close(conn.pings[frame.PingID])
			delete(conn.pings, frame.PingID)
			conn.Unlock()
		} else {
			debug.Println("Received PING. Replying...")
			conn.output[0] <- frame
		}

	case *frames.GOAWAY:
		lastProcessed := frame.LastGoodStreamID
		for streamID, stream := range conn.streams {
			if streamID&1 == conn.oddity && streamID > lastProcessed {
				// Stream is locally-sent and has not been processed.
				// TODO: Inform the server that the push has not been successful.
				stream.Close()
			}
		}
		conn.goawayReceived = true

	case *frames.HEADERS:
		conn.handleHeaders(frame)

	case *frames.WINDOW_UPDATE:
		// Ignore.

	case *frames.DATA:
		if conn.server == nil {
			conn.handleServerData(frame)
		} else {
			conn.handleClientData(frame)
		}

	default:
		log.Println(fmt.Sprintf("Ignored unexpected frame type %T", frame))
	}
	return false
}

// readFrames is the main processing loop, where frames
// are read from the connection and processed individually.
// Returning from readFrames begins the cleanup and exit
// process for this connection.
func (conn *Conn) readFrames() {
	// Ensure no panics happen.
	defer func() {
		if v := recover(); v != nil {
			if !conn.closed() {
				log.Println("Encountered receive error:", v)
			}
		}
	}()

	for {

		// This is the mechanism for handling too many benign errors.
		// By default MaxBenignErrors is 0, which ignores errors.
		if conn.numBenignErrors > common.MaxBenignErrors && common.MaxBenignErrors > 0 {
			log.Println("Warning: Too many invalid stream IDs received. Ending connection.")
			conn.protocolError(0)
			return
		}

		// ReadFrame takes care of the frame parsing for us.
		conn.refreshReadTimeout()
		frame, err := frames.ReadFrame(conn.buf)
		if err != nil {
			conn.handleReadWriteError(err)
			return
		}

		// Print frame type.
		debug.Printf("Receiving %s:\n", frame.Name())

		// Decompress the frame's headers, if there are any.
		err = frame.Decompress(conn.decompressor)
		if err != nil {
			log.Printf("Error in decompression: %v (%T).\n", err, frame)
			conn.protocolError(0)
			return
		}

		// Print frame once the content's been decompressed.
		debug.Println(frame)

		// This is the main frame handling.
		if conn.processFrame(frame) {
			return
		}
	}
}

// send is run in a separate goroutine. It's used
// to ensure clear interleaving of frames and to
// provide assurances of priority and structure.
func (conn *Conn) send() {
	// Catch any panics.
	defer func() {
		if v := recover(); v != nil {
			if !conn.closed() {
				log.Println("Encountered send error:", v)
			}
		}
	}()

	// Enter the processing loop.
	i := 1
	for {

		// Once per 5 frames, pick randomly.
		var frame common.Frame
		if i == 0 { // Ignore priority.
			frame = conn.selectFrameToSend(false)
		} else { // Normal selection.
			frame = conn.selectFrameToSend(true)
		}

		i++
		if i >= 5 {
			i = 0
		}

		if frame == nil {
			conn.Close()
			return
		}

		// Compress any name/value header blocks.
		err := frame.Compress(conn.compressor)
		if err != nil {
			log.Printf("Error in compression: %v (%T).\n", err, frame)
			return
		}

		debug.Printf("Sending %s:\n", frame.Name())
		debug.Println(frame)

		// Leave the specifics of writing to the
		// connection up to the frame.
		conn.refreshWriteTimeout()
		_, err = frame.WriteTo(conn.conn)
		if err != nil {
			conn.handleReadWriteError(err)
			return
		}
	}
}

// selectFrameToSend follows the specification's guidance
// on frame priority, sending frames with higher priority
// (a smaller number) first. If the given boolean is false,
// this priority is temporarily ignored, which can be used
// when high load is ignoring low-priority frames.
func (conn *Conn) selectFrameToSend(prioritise bool) (frame common.Frame) {
	if conn.closed() {
		return nil
	}

	// Try in priority order first.
	if prioritise {
		for i := 0; i < 8; i++ {
			select {
			case frame = <-conn.output[i]:
				return frame
			default:
			}
		}

		// No frames are immediately pending, so if the
		// connection is being closed, cease sending
		// safely.
		conn.sendingLock.Lock()
		if conn.sending != nil {
			close(conn.sending)
			conn.sendingLock.Unlock()
			runtime.Goexit()
		}
		conn.sendingLock.Unlock()
	}

	// Wait for any frame.
	select {
	case frame = <-conn.output[0]:
		return frame
	case frame = <-conn.output[1]:
		return frame
	case frame = <-conn.output[2]:
		return frame
	case frame = <-conn.output[2]:
		return frame
	case frame = <-conn.output[4]:
		return frame
	case frame = <-conn.output[5]:
		return frame
	case frame = <-conn.output[6]:
		return frame
	case frame = <-conn.output[7]:
		return frame
	case _ = <-conn.stop:
		return nil
	}
}

// Add timeouts if requested by the server.
func (conn *Conn) refreshTimeouts() {
	conn.connLock.Lock()
	if d := conn.readTimeout; d != 0 && conn.conn != nil {
		conn.conn.SetReadDeadline(time.Now().Add(d))
	}
	if d := conn.writeTimeout; d != 0 && conn.conn != nil {
		conn.conn.SetWriteDeadline(time.Now().Add(d))
	}
	conn.connLock.Unlock()
}

// Add timeouts if requested by the server.
func (conn *Conn) refreshReadTimeout() {
	conn.connLock.Lock()
	if d := conn.readTimeout; d != 0 && conn.conn != nil {
		conn.conn.SetReadDeadline(time.Now().Add(d))
	}
	conn.connLock.Unlock()
}

// Add timeouts if requested by the server.
func (conn *Conn) refreshWriteTimeout() {
	conn.connLock.Lock()
	if d := conn.writeTimeout; d != 0 && conn.conn != nil {
		conn.conn.SetWriteDeadline(time.Now().Add(d))
	}
	conn.connLock.Unlock()
}