package spdy

import (
	"sync"
)

const SPDY_VERSION = 3

var DebugMode = false
var MaxBenignErrors = 10

// Control types
const (
	CONTROL_FRAME = -1
	DATA_FRAME    = -2
)

// Frame types
const (
	SYN_STREAM    = 1
	SYN_REPLY     = 2
	RST_STREAM    = 3
	SETTINGS      = 4
	PING          = 6
	GOAWAY        = 7
	HEADERS       = 8
	WINDOW_UPDATE = 9
	CREDENTIAL    = 10
)

// Flags
const (
	FLAG_FIN                     = 1
	FLAG_UNIDIRECTIONAL          = 2
	FLAG_SETTINGS_CLEAR_SETTINGS = 1
	FLAG_SETTINGS_PERSIST_VALUE  = 1
	FLAG_SETTINGS_PERSISTED      = 2
)

// RST_STREAM status codes
const (
	RST_STREAM_PROTOCOL_ERROR        = 1
	RST_STREAM_INVALID_STREAM        = 2
	RST_STREAM_REFUSED_STREAM        = 3
	RST_STREAM_UNSUPPORTED_VERSION   = 4
	RST_STREAM_CANCEL                = 5
	RST_STREAM_INTERNAL_ERROR        = 6
	RST_STREAM_FLOW_CONTROL_ERROR    = 7
	RST_STREAM_STREAM_IN_USE         = 8
	RST_STREAM_STREAM_ALREADY_CLOSED = 9
	RST_STREAM_INVALID_CREDENTIALS   = 10
	RST_STREAM_FRAME_TOO_LARGE       = 11
)

var statusCodeText = map[int]string{
	RST_STREAM_PROTOCOL_ERROR:        "PROTOCOL_ERROR",
	RST_STREAM_INVALID_STREAM:        "INVALID_STREAM",
	RST_STREAM_REFUSED_STREAM:        "REFUSED_STREAM",
	RST_STREAM_UNSUPPORTED_VERSION:   "UNSUPPORTED_VERSION",
	RST_STREAM_CANCEL:                "CANCEL",
	RST_STREAM_INTERNAL_ERROR:        "INTERNAL_ERROR",
	RST_STREAM_FLOW_CONTROL_ERROR:    "FLOW_CONTROL_ERROR",
	RST_STREAM_STREAM_IN_USE:         "STREAM_IN_USE",
	RST_STREAM_STREAM_ALREADY_CLOSED: "STREAM_ALREADY_CLOSED",
	RST_STREAM_INVALID_CREDENTIALS:   "INVALID_CREDENTIALS",
	RST_STREAM_FRAME_TOO_LARGE:       "FRAME_TOO_LARGE",
}

func StatusCodeText(code int) string {
	return statusCodeText[code]
}

func StatusCodeIsFatal(code int) bool {
	switch code {
	case RST_STREAM_PROTOCOL_ERROR:
		return true
	case RST_STREAM_INTERNAL_ERROR:
		return true
	case RST_STREAM_FRAME_TOO_LARGE:
		return true
	case RST_STREAM_UNSUPPORTED_VERSION:
		return true

	default:
		return false
	}
}

// Settings IDs
const (
	SETTINGS_UPLOAD_BANDWIDTH               = 1
	SETTINGS_DOWNLOAD_BANDWIDTH             = 2
	SETTINGS_ROUND_TRIP_TIME                = 3
	SETTINGS_MAX_CONCURRENT_STREAMS         = 4
	SETTINGS_CURRENT_CWND                   = 5
	SETTINGS_DOWNLOAD_RETRANS_RATE          = 6
	SETTINGS_INITIAL_WINDOW_SIZE            = 7
	SETTINGS_CLIENT_CERTIFICATE_VECTOR_SIZE = 8
)

// Stream state
type StreamState struct {
	sync.RWMutex
	s uint8
}

func (s *StreamState) Open() bool {
	s.RLock()
	defer s.RUnlock()
	return s.s == STATE_OPEN
}

func (s *StreamState) Closed() bool {
	s.RLock()
	defer s.RUnlock()
	return s.s == STATE_CLOSED
}

func (s *StreamState) ClosedThere() bool {
	s.RLock()
	defer s.RUnlock()
	return s.s == STATE_CLOSED || s.s == STATE_HALF_CLOSED_THERE
}

func (s *StreamState) OpenThere() bool {
	return !s.ClosedThere()
}

func (s *StreamState) ClosedHere() bool {
	s.RLock()
	defer s.RUnlock()
	return s.s == STATE_CLOSED || s.s == STATE_HALF_CLOSED_HERE
}

func (s *StreamState) OpenHere() bool {
	return !s.ClosedHere()
}

func (s *StreamState) Close() {
	s.Lock()
	s.s = STATE_CLOSED
	s.Unlock()
}

func (s *StreamState) CloseHere() {
	s.Lock()
	if s.s == STATE_OPEN {
		s.s = STATE_HALF_CLOSED_HERE
	} else if s.s == STATE_HALF_CLOSED_THERE {
		s.s = STATE_CLOSED
	}
	s.Unlock()
}

func (s *StreamState) CloseThere() {
	s.Lock()
	if s.s == STATE_OPEN {
		s.s = STATE_HALF_CLOSED_THERE
	} else if s.s == STATE_HALF_CLOSED_HERE {
		s.s = STATE_CLOSED
	}
	s.Unlock()
}

const (
	STATE_OPEN uint8 = iota
	STATE_HALF_CLOSED_HERE
	STATE_HALF_CLOSED_THERE
	STATE_CLOSED
)

// Stream priority values.
const (
	MAX_PRIORITY = 0
	MIN_PRIORITY = 7
)

// HTTP time format.
const TimeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

// Maximum frame size (2 ** 24 -1).
const MAX_FRAME_SIZE = 0xffffff

const MAX_DATA_SIZE = 0xffffff

// Maximum stream ID (2 ** 31 -1).
const MAX_STREAM_ID = 0x7fffffff

// Maximum number of bytes in the transfer window.
const MAX_TRANSFER_WINDOW_SIZE = 0x80000000

const DEFAULT_INITIAL_WINDOW_SIZE = 65536

// Maximum delta window size field for WINDOW_UPDATE.
const MAX_DELTA_WINDOW_SIZE = 0x7fffffff

// Compression header for SPDY/2
var HeaderDictionaryV2 = []byte{
	0x6f, 0x70, 0x74, 0x69, 0x6f, 0x6e, 0x73, 0x67,
	0x65, 0x74, 0x68, 0x65, 0x61, 0x64, 0x70, 0x6f,
	0x73, 0x74, 0x70, 0x75, 0x74, 0x64, 0x65, 0x6c,
	0x65, 0x74, 0x65, 0x74, 0x72, 0x61, 0x63, 0x65,
	0x61, 0x63, 0x63, 0x65, 0x70, 0x74, 0x61, 0x63,
	0x63, 0x65, 0x70, 0x74, 0x2d, 0x63, 0x68, 0x61,
	0x72, 0x73, 0x65, 0x74, 0x61, 0x63, 0x63, 0x65,
	0x70, 0x74, 0x2d, 0x65, 0x6e, 0x63, 0x6f, 0x64,
	0x69, 0x6e, 0x67, 0x61, 0x63, 0x63, 0x65, 0x70,
	0x74, 0x2d, 0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61,
	0x67, 0x65, 0x61, 0x75, 0x74, 0x68, 0x6f, 0x72,
	0x69, 0x7a, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x65,
	0x78, 0x70, 0x65, 0x63, 0x74, 0x66, 0x72, 0x6f,
	0x6d, 0x68, 0x6f, 0x73, 0x74, 0x69, 0x66, 0x2d,
	0x6d, 0x6f, 0x64, 0x69, 0x66, 0x69, 0x65, 0x64,
	0x2d, 0x73, 0x69, 0x6e, 0x63, 0x65, 0x69, 0x66,
	0x2d, 0x6d, 0x61, 0x74, 0x63, 0x68, 0x69, 0x66,
	0x2d, 0x6e, 0x6f, 0x6e, 0x65, 0x2d, 0x6d, 0x61,
	0x74, 0x63, 0x68, 0x69, 0x66, 0x2d, 0x72, 0x61,
	0x6e, 0x67, 0x65, 0x69, 0x66, 0x2d, 0x75, 0x6e,
	0x6d, 0x6f, 0x64, 0x69, 0x66, 0x69, 0x65, 0x64,
	0x73, 0x69, 0x6e, 0x63, 0x65, 0x6d, 0x61, 0x78,
	0x2d, 0x66, 0x6f, 0x72, 0x77, 0x61, 0x72, 0x64,
	0x73, 0x70, 0x72, 0x6f, 0x78, 0x79, 0x2d, 0x61,
	0x75, 0x74, 0x68, 0x6f, 0x72, 0x69, 0x7a, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x72, 0x61, 0x6e, 0x67,
	0x65, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x72,
	0x74, 0x65, 0x75, 0x73, 0x65, 0x72, 0x2d, 0x61,
	0x67, 0x65, 0x6e, 0x74, 0x31, 0x30, 0x30, 0x31,
	0x30, 0x31, 0x32, 0x30, 0x30, 0x32, 0x30, 0x31,
	0x32, 0x30, 0x32, 0x32, 0x30, 0x33, 0x32, 0x30,
	0x34, 0x32, 0x30, 0x35, 0x32, 0x30, 0x36, 0x33,
	0x30, 0x30, 0x33, 0x30, 0x31, 0x33, 0x30, 0x32,
	0x33, 0x30, 0x33, 0x33, 0x30, 0x34, 0x33, 0x30,
	0x35, 0x33, 0x30, 0x36, 0x33, 0x30, 0x37, 0x34,
	0x30, 0x30, 0x34, 0x30, 0x31, 0x34, 0x30, 0x32,
	0x34, 0x30, 0x33, 0x34, 0x30, 0x34, 0x34, 0x30,
	0x35, 0x34, 0x30, 0x36, 0x34, 0x30, 0x37, 0x34,
	0x30, 0x38, 0x34, 0x30, 0x39, 0x34, 0x31, 0x30,
	0x34, 0x31, 0x31, 0x34, 0x31, 0x32, 0x34, 0x31,
	0x33, 0x34, 0x31, 0x34, 0x34, 0x31, 0x35, 0x34,
	0x31, 0x36, 0x34, 0x31, 0x37, 0x35, 0x30, 0x30,
	0x35, 0x30, 0x31, 0x35, 0x30, 0x32, 0x35, 0x30,
	0x33, 0x35, 0x30, 0x34, 0x35, 0x30, 0x35, 0x61,
	0x63, 0x63, 0x65, 0x70, 0x74, 0x2d, 0x72, 0x61,
	0x6e, 0x67, 0x65, 0x73, 0x61, 0x67, 0x65, 0x65,
	0x74, 0x61, 0x67, 0x6c, 0x6f, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x70, 0x72, 0x6f, 0x78, 0x79,
	0x2d, 0x61, 0x75, 0x74, 0x68, 0x65, 0x6e, 0x74,
	0x69, 0x63, 0x61, 0x74, 0x65, 0x70, 0x75, 0x62,
	0x6c, 0x69, 0x63, 0x72, 0x65, 0x74, 0x72, 0x79,
	0x2d, 0x61, 0x66, 0x74, 0x65, 0x72, 0x73, 0x65,
	0x72, 0x76, 0x65, 0x72, 0x76, 0x61, 0x72, 0x79,
	0x77, 0x61, 0x72, 0x6e, 0x69, 0x6e, 0x67, 0x77,
	0x77, 0x77, 0x2d, 0x61, 0x75, 0x74, 0x68, 0x65,
	0x6e, 0x74, 0x69, 0x63, 0x61, 0x74, 0x65, 0x61,
	0x6c, 0x6c, 0x6f, 0x77, 0x63, 0x6f, 0x6e, 0x74,
	0x65, 0x6e, 0x74, 0x2d, 0x62, 0x61, 0x73, 0x65,
	0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2d,
	0x65, 0x6e, 0x63, 0x6f, 0x64, 0x69, 0x6e, 0x67,
	0x63, 0x61, 0x63, 0x68, 0x65, 0x2d, 0x63, 0x6f,
	0x6e, 0x74, 0x72, 0x6f, 0x6c, 0x63, 0x6f, 0x6e,
	0x6e, 0x65, 0x63, 0x74, 0x69, 0x6f, 0x6e, 0x64,
	0x61, 0x74, 0x65, 0x74, 0x72, 0x61, 0x69, 0x6c,
	0x65, 0x72, 0x74, 0x72, 0x61, 0x6e, 0x73, 0x66,
	0x65, 0x72, 0x2d, 0x65, 0x6e, 0x63, 0x6f, 0x64,
	0x69, 0x6e, 0x67, 0x75, 0x70, 0x67, 0x72, 0x61,
	0x64, 0x65, 0x76, 0x69, 0x61, 0x77, 0x61, 0x72,
	0x6e, 0x69, 0x6e, 0x67, 0x63, 0x6f, 0x6e, 0x74,
	0x65, 0x6e, 0x74, 0x2d, 0x6c, 0x61, 0x6e, 0x67,
	0x75, 0x61, 0x67, 0x65, 0x63, 0x6f, 0x6e, 0x74,
	0x65, 0x6e, 0x74, 0x2d, 0x6c, 0x65, 0x6e, 0x67,
	0x74, 0x68, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e,
	0x74, 0x2d, 0x6c, 0x6f, 0x63, 0x61, 0x74, 0x69,
	0x6f, 0x6e, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e,
	0x74, 0x2d, 0x6d, 0x64, 0x35, 0x63, 0x6f, 0x6e,
	0x74, 0x65, 0x6e, 0x74, 0x2d, 0x72, 0x61, 0x6e,
	0x67, 0x65, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e,
	0x74, 0x2d, 0x74, 0x79, 0x70, 0x65, 0x65, 0x74,
	0x61, 0x67, 0x65, 0x78, 0x70, 0x69, 0x72, 0x65,
	0x73, 0x6c, 0x61, 0x73, 0x74, 0x2d, 0x6d, 0x6f,
	0x64, 0x69, 0x66, 0x69, 0x65, 0x64, 0x73, 0x65,
	0x74, 0x2d, 0x63, 0x6f, 0x6f, 0x6b, 0x69, 0x65,
	0x4d, 0x6f, 0x6e, 0x64, 0x61, 0x79, 0x54, 0x75,
	0x65, 0x73, 0x64, 0x61, 0x79, 0x57, 0x65, 0x64,
	0x6e, 0x65, 0x73, 0x64, 0x61, 0x79, 0x54, 0x68,
	0x75, 0x72, 0x73, 0x64, 0x61, 0x79, 0x46, 0x72,
	0x69, 0x64, 0x61, 0x79, 0x53, 0x61, 0x74, 0x75,
	0x72, 0x64, 0x61, 0x79, 0x53, 0x75, 0x6e, 0x64,
	0x61, 0x79, 0x4a, 0x61, 0x6e, 0x46, 0x65, 0x62,
	0x4d, 0x61, 0x72, 0x41, 0x70, 0x72, 0x4d, 0x61,
	0x79, 0x4a, 0x75, 0x6e, 0x4a, 0x75, 0x6c, 0x41,
	0x75, 0x67, 0x53, 0x65, 0x70, 0x4f, 0x63, 0x74,
	0x4e, 0x6f, 0x76, 0x44, 0x65, 0x63, 0x63, 0x68,
	0x75, 0x6e, 0x6b, 0x65, 0x64, 0x74, 0x65, 0x78,
	0x74, 0x2f, 0x68, 0x74, 0x6d, 0x6c, 0x69, 0x6d,
	0x61, 0x67, 0x65, 0x2f, 0x70, 0x6e, 0x67, 0x69,
	0x6d, 0x61, 0x67, 0x65, 0x2f, 0x6a, 0x70, 0x67,
	0x69, 0x6d, 0x61, 0x67, 0x65, 0x2f, 0x67, 0x69,
	0x66, 0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61,
	0x74, 0x69, 0x6f, 0x6e, 0x2f, 0x78, 0x6d, 0x6c,
	0x61, 0x70, 0x70, 0x6c, 0x69, 0x63, 0x61, 0x74,
	0x69, 0x6f, 0x6e, 0x2f, 0x78, 0x68, 0x74, 0x6d,
	0x6c, 0x74, 0x65, 0x78, 0x74, 0x2f, 0x70, 0x6c,
	0x61, 0x69, 0x6e, 0x70, 0x75, 0x62, 0x6c, 0x69,
	0x63, 0x6d, 0x61, 0x78, 0x2d, 0x61, 0x67, 0x65,
	0x63, 0x68, 0x61, 0x72, 0x73, 0x65, 0x74, 0x3d,
	0x69, 0x73, 0x6f, 0x2d, 0x38, 0x38, 0x35, 0x39,
	0x2d, 0x31, 0x75, 0x74, 0x66, 0x2d, 0x38, 0x67,
	0x7a, 0x69, 0x70, 0x64, 0x65, 0x66, 0x6c, 0x61,
	0x74, 0x65, 0x48, 0x54, 0x54, 0x50, 0x2f, 0x31,
	0x2e, 0x31, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73,
	0x76, 0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x75,
	0x72, 0x6c, 0x00,
}

// Compression header for SPDY/3
var HeaderDictionaryV3 = []byte{
	0x00, 0x00, 0x00, 0x07, 0x6f, 0x70, 0x74, 0x69, // - - - - o p t i
	0x6f, 0x6e, 0x73, 0x00, 0x00, 0x00, 0x04, 0x68, // o n s - - - - h
	0x65, 0x61, 0x64, 0x00, 0x00, 0x00, 0x04, 0x70, // e a d - - - - p
	0x6f, 0x73, 0x74, 0x00, 0x00, 0x00, 0x03, 0x70, // o s t - - - - p
	0x75, 0x74, 0x00, 0x00, 0x00, 0x06, 0x64, 0x65, // u t - - - - d e
	0x6c, 0x65, 0x74, 0x65, 0x00, 0x00, 0x00, 0x05, // l e t e - - - -
	0x74, 0x72, 0x61, 0x63, 0x65, 0x00, 0x00, 0x00, // t r a c e - - -
	0x06, 0x61, 0x63, 0x63, 0x65, 0x70, 0x74, 0x00, // - a c c e p t -
	0x00, 0x00, 0x0e, 0x61, 0x63, 0x63, 0x65, 0x70, // - - - a c c e p
	0x74, 0x2d, 0x63, 0x68, 0x61, 0x72, 0x73, 0x65, // t - c h a r s e
	0x74, 0x00, 0x00, 0x00, 0x0f, 0x61, 0x63, 0x63, // t - - - - a c c
	0x65, 0x70, 0x74, 0x2d, 0x65, 0x6e, 0x63, 0x6f, // e p t - e n c o
	0x64, 0x69, 0x6e, 0x67, 0x00, 0x00, 0x00, 0x0f, // d i n g - - - -
	0x61, 0x63, 0x63, 0x65, 0x70, 0x74, 0x2d, 0x6c, // a c c e p t - l
	0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, 0x00, // a n g u a g e -
	0x00, 0x00, 0x0d, 0x61, 0x63, 0x63, 0x65, 0x70, // - - - a c c e p
	0x74, 0x2d, 0x72, 0x61, 0x6e, 0x67, 0x65, 0x73, // t - r a n g e s
	0x00, 0x00, 0x00, 0x03, 0x61, 0x67, 0x65, 0x00, // - - - - a g e -
	0x00, 0x00, 0x05, 0x61, 0x6c, 0x6c, 0x6f, 0x77, // - - - a l l o w
	0x00, 0x00, 0x00, 0x0d, 0x61, 0x75, 0x74, 0x68, // - - - - a u t h
	0x6f, 0x72, 0x69, 0x7a, 0x61, 0x74, 0x69, 0x6f, // o r i z a t i o
	0x6e, 0x00, 0x00, 0x00, 0x0d, 0x63, 0x61, 0x63, // n - - - - c a c
	0x68, 0x65, 0x2d, 0x63, 0x6f, 0x6e, 0x74, 0x72, // h e - c o n t r
	0x6f, 0x6c, 0x00, 0x00, 0x00, 0x0a, 0x63, 0x6f, // o l - - - - c o
	0x6e, 0x6e, 0x65, 0x63, 0x74, 0x69, 0x6f, 0x6e, // n n e c t i o n
	0x00, 0x00, 0x00, 0x0c, 0x63, 0x6f, 0x6e, 0x74, // - - - - c o n t
	0x65, 0x6e, 0x74, 0x2d, 0x62, 0x61, 0x73, 0x65, // e n t - b a s e
	0x00, 0x00, 0x00, 0x10, 0x63, 0x6f, 0x6e, 0x74, // - - - - c o n t
	0x65, 0x6e, 0x74, 0x2d, 0x65, 0x6e, 0x63, 0x6f, // e n t - e n c o
	0x64, 0x69, 0x6e, 0x67, 0x00, 0x00, 0x00, 0x10, // d i n g - - - -
	0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2d, // c o n t e n t -
	0x6c, 0x61, 0x6e, 0x67, 0x75, 0x61, 0x67, 0x65, // l a n g u a g e
	0x00, 0x00, 0x00, 0x0e, 0x63, 0x6f, 0x6e, 0x74, // - - - - c o n t
	0x65, 0x6e, 0x74, 0x2d, 0x6c, 0x65, 0x6e, 0x67, // e n t - l e n g
	0x74, 0x68, 0x00, 0x00, 0x00, 0x10, 0x63, 0x6f, // t h - - - - c o
	0x6e, 0x74, 0x65, 0x6e, 0x74, 0x2d, 0x6c, 0x6f, // n t e n t - l o
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x00, 0x00, // c a t i o n - -
	0x00, 0x0b, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, // - - c o n t e n
	0x74, 0x2d, 0x6d, 0x64, 0x35, 0x00, 0x00, 0x00, // t - m d 5 - - -
	0x0d, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, 0x74, // - c o n t e n t
	0x2d, 0x72, 0x61, 0x6e, 0x67, 0x65, 0x00, 0x00, // - r a n g e - -
	0x00, 0x0c, 0x63, 0x6f, 0x6e, 0x74, 0x65, 0x6e, // - - c o n t e n
	0x74, 0x2d, 0x74, 0x79, 0x70, 0x65, 0x00, 0x00, // t - t y p e - -
	0x00, 0x04, 0x64, 0x61, 0x74, 0x65, 0x00, 0x00, // - - d a t e - -
	0x00, 0x04, 0x65, 0x74, 0x61, 0x67, 0x00, 0x00, // - - e t a g - -
	0x00, 0x06, 0x65, 0x78, 0x70, 0x65, 0x63, 0x74, // - - e x p e c t
	0x00, 0x00, 0x00, 0x07, 0x65, 0x78, 0x70, 0x69, // - - - - e x p i
	0x72, 0x65, 0x73, 0x00, 0x00, 0x00, 0x04, 0x66, // r e s - - - - f
	0x72, 0x6f, 0x6d, 0x00, 0x00, 0x00, 0x04, 0x68, // r o m - - - - h
	0x6f, 0x73, 0x74, 0x00, 0x00, 0x00, 0x08, 0x69, // o s t - - - - i
	0x66, 0x2d, 0x6d, 0x61, 0x74, 0x63, 0x68, 0x00, // f - m a t c h -
	0x00, 0x00, 0x11, 0x69, 0x66, 0x2d, 0x6d, 0x6f, // - - - i f - m o
	0x64, 0x69, 0x66, 0x69, 0x65, 0x64, 0x2d, 0x73, // d i f i e d - s
	0x69, 0x6e, 0x63, 0x65, 0x00, 0x00, 0x00, 0x0d, // i n c e - - - -
	0x69, 0x66, 0x2d, 0x6e, 0x6f, 0x6e, 0x65, 0x2d, // i f - n o n e -
	0x6d, 0x61, 0x74, 0x63, 0x68, 0x00, 0x00, 0x00, // m a t c h - - -
	0x08, 0x69, 0x66, 0x2d, 0x72, 0x61, 0x6e, 0x67, // - i f - r a n g
	0x65, 0x00, 0x00, 0x00, 0x13, 0x69, 0x66, 0x2d, // e - - - - i f -
	0x75, 0x6e, 0x6d, 0x6f, 0x64, 0x69, 0x66, 0x69, // u n m o d i f i
	0x65, 0x64, 0x2d, 0x73, 0x69, 0x6e, 0x63, 0x65, // e d - s i n c e
	0x00, 0x00, 0x00, 0x0d, 0x6c, 0x61, 0x73, 0x74, // - - - - l a s t
	0x2d, 0x6d, 0x6f, 0x64, 0x69, 0x66, 0x69, 0x65, // - m o d i f i e
	0x64, 0x00, 0x00, 0x00, 0x08, 0x6c, 0x6f, 0x63, // d - - - - l o c
	0x61, 0x74, 0x69, 0x6f, 0x6e, 0x00, 0x00, 0x00, // a t i o n - - -
	0x0c, 0x6d, 0x61, 0x78, 0x2d, 0x66, 0x6f, 0x72, // - m a x - f o r
	0x77, 0x61, 0x72, 0x64, 0x73, 0x00, 0x00, 0x00, // w a r d s - - -
	0x06, 0x70, 0x72, 0x61, 0x67, 0x6d, 0x61, 0x00, // - p r a g m a -
	0x00, 0x00, 0x12, 0x70, 0x72, 0x6f, 0x78, 0x79, // - - - p r o x y
	0x2d, 0x61, 0x75, 0x74, 0x68, 0x65, 0x6e, 0x74, // - a u t h e n t
	0x69, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00, 0x00, // i c a t e - - -
	0x13, 0x70, 0x72, 0x6f, 0x78, 0x79, 0x2d, 0x61, // - p r o x y - a
	0x75, 0x74, 0x68, 0x6f, 0x72, 0x69, 0x7a, 0x61, // u t h o r i z a
	0x74, 0x69, 0x6f, 0x6e, 0x00, 0x00, 0x00, 0x05, // t i o n - - - -
	0x72, 0x61, 0x6e, 0x67, 0x65, 0x00, 0x00, 0x00, // r a n g e - - -
	0x07, 0x72, 0x65, 0x66, 0x65, 0x72, 0x65, 0x72, // - r e f e r e r
	0x00, 0x00, 0x00, 0x0b, 0x72, 0x65, 0x74, 0x72, // - - - - r e t r
	0x79, 0x2d, 0x61, 0x66, 0x74, 0x65, 0x72, 0x00, // y - a f t e r -
	0x00, 0x00, 0x06, 0x73, 0x65, 0x72, 0x76, 0x65, // - - - s e r v e
	0x72, 0x00, 0x00, 0x00, 0x02, 0x74, 0x65, 0x00, // r - - - - t e -
	0x00, 0x00, 0x07, 0x74, 0x72, 0x61, 0x69, 0x6c, // - - - t r a i l
	0x65, 0x72, 0x00, 0x00, 0x00, 0x11, 0x74, 0x72, // e r - - - - t r
	0x61, 0x6e, 0x73, 0x66, 0x65, 0x72, 0x2d, 0x65, // a n s f e r - e
	0x6e, 0x63, 0x6f, 0x64, 0x69, 0x6e, 0x67, 0x00, // n c o d i n g -
	0x00, 0x00, 0x07, 0x75, 0x70, 0x67, 0x72, 0x61, // - - - u p g r a
	0x64, 0x65, 0x00, 0x00, 0x00, 0x0a, 0x75, 0x73, // d e - - - - u s
	0x65, 0x72, 0x2d, 0x61, 0x67, 0x65, 0x6e, 0x74, // e r - a g e n t
	0x00, 0x00, 0x00, 0x04, 0x76, 0x61, 0x72, 0x79, // - - - - v a r y
	0x00, 0x00, 0x00, 0x03, 0x76, 0x69, 0x61, 0x00, // - - - - v i a -
	0x00, 0x00, 0x07, 0x77, 0x61, 0x72, 0x6e, 0x69, // - - - w a r n i
	0x6e, 0x67, 0x00, 0x00, 0x00, 0x10, 0x77, 0x77, // n g - - - - w w
	0x77, 0x2d, 0x61, 0x75, 0x74, 0x68, 0x65, 0x6e, // w - a u t h e n
	0x74, 0x69, 0x63, 0x61, 0x74, 0x65, 0x00, 0x00, // t i c a t e - -
	0x00, 0x06, 0x6d, 0x65, 0x74, 0x68, 0x6f, 0x64, // - - m e t h o d
	0x00, 0x00, 0x00, 0x03, 0x67, 0x65, 0x74, 0x00, // - - - - g e t -
	0x00, 0x00, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, // - - - s t a t u
	0x73, 0x00, 0x00, 0x00, 0x06, 0x32, 0x30, 0x30, // s - - - - 2 0 0
	0x20, 0x4f, 0x4b, 0x00, 0x00, 0x00, 0x07, 0x76, // - O K - - - - v
	0x65, 0x72, 0x73, 0x69, 0x6f, 0x6e, 0x00, 0x00, // e r s i o n - -
	0x00, 0x08, 0x48, 0x54, 0x54, 0x50, 0x2f, 0x31, // - - H T T P - 1
	0x2e, 0x31, 0x00, 0x00, 0x00, 0x03, 0x75, 0x72, // - 1 - - - - u r
	0x6c, 0x00, 0x00, 0x00, 0x06, 0x70, 0x75, 0x62, // l - - - - p u b
	0x6c, 0x69, 0x63, 0x00, 0x00, 0x00, 0x0a, 0x73, // l i c - - - - s
	0x65, 0x74, 0x2d, 0x63, 0x6f, 0x6f, 0x6b, 0x69, // e t - c o o k i
	0x65, 0x00, 0x00, 0x00, 0x0a, 0x6b, 0x65, 0x65, // e - - - - k e e
	0x70, 0x2d, 0x61, 0x6c, 0x69, 0x76, 0x65, 0x00, // p - a l i v e -
	0x00, 0x00, 0x06, 0x6f, 0x72, 0x69, 0x67, 0x69, // - - - o r i g i
	0x6e, 0x31, 0x30, 0x30, 0x31, 0x30, 0x31, 0x32, // n 1 0 0 1 0 1 2
	0x30, 0x31, 0x32, 0x30, 0x32, 0x32, 0x30, 0x35, // 0 1 2 0 2 2 0 5
	0x32, 0x30, 0x36, 0x33, 0x30, 0x30, 0x33, 0x30, // 2 0 6 3 0 0 3 0
	0x32, 0x33, 0x30, 0x33, 0x33, 0x30, 0x34, 0x33, // 2 3 0 3 3 0 4 3
	0x30, 0x35, 0x33, 0x30, 0x36, 0x33, 0x30, 0x37, // 0 5 3 0 6 3 0 7
	0x34, 0x30, 0x32, 0x34, 0x30, 0x35, 0x34, 0x30, // 4 0 2 4 0 5 4 0
	0x36, 0x34, 0x30, 0x37, 0x34, 0x30, 0x38, 0x34, // 6 4 0 7 4 0 8 4
	0x30, 0x39, 0x34, 0x31, 0x30, 0x34, 0x31, 0x31, // 0 9 4 1 0 4 1 1
	0x34, 0x31, 0x32, 0x34, 0x31, 0x33, 0x34, 0x31, // 4 1 2 4 1 3 4 1
	0x34, 0x34, 0x31, 0x35, 0x34, 0x31, 0x36, 0x34, // 4 4 1 5 4 1 6 4
	0x31, 0x37, 0x35, 0x30, 0x32, 0x35, 0x30, 0x34, // 1 7 5 0 2 5 0 4
	0x35, 0x30, 0x35, 0x32, 0x30, 0x33, 0x20, 0x4e, // 5 0 5 2 0 3 - N
	0x6f, 0x6e, 0x2d, 0x41, 0x75, 0x74, 0x68, 0x6f, // o n - A u t h o
	0x72, 0x69, 0x74, 0x61, 0x74, 0x69, 0x76, 0x65, // r i t a t i v e
	0x20, 0x49, 0x6e, 0x66, 0x6f, 0x72, 0x6d, 0x61, // - I n f o r m a
	0x74, 0x69, 0x6f, 0x6e, 0x32, 0x30, 0x34, 0x20, // t i o n 2 0 4 -
	0x4e, 0x6f, 0x20, 0x43, 0x6f, 0x6e, 0x74, 0x65, // N o - C o n t e
	0x6e, 0x74, 0x33, 0x30, 0x31, 0x20, 0x4d, 0x6f, // n t 3 0 1 - M o
	0x76, 0x65, 0x64, 0x20, 0x50, 0x65, 0x72, 0x6d, // v e d - P e r m
	0x61, 0x6e, 0x65, 0x6e, 0x74, 0x6c, 0x79, 0x34, // a n e n t l y 4
	0x30, 0x30, 0x20, 0x42, 0x61, 0x64, 0x20, 0x52, // 0 0 - B a d - R
	0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x34, 0x30, // e q u e s t 4 0
	0x31, 0x20, 0x55, 0x6e, 0x61, 0x75, 0x74, 0x68, // 1 - U n a u t h
	0x6f, 0x72, 0x69, 0x7a, 0x65, 0x64, 0x34, 0x30, // o r i z e d 4 0
	0x33, 0x20, 0x46, 0x6f, 0x72, 0x62, 0x69, 0x64, // 3 - F o r b i d
	0x64, 0x65, 0x6e, 0x34, 0x30, 0x34, 0x20, 0x4e, // d e n 4 0 4 - N
	0x6f, 0x74, 0x20, 0x46, 0x6f, 0x75, 0x6e, 0x64, // o t - F o u n d
	0x35, 0x30, 0x30, 0x20, 0x49, 0x6e, 0x74, 0x65, // 5 0 0 - I n t e
	0x72, 0x6e, 0x61, 0x6c, 0x20, 0x53, 0x65, 0x72, // r n a l - S e r
	0x76, 0x65, 0x72, 0x20, 0x45, 0x72, 0x72, 0x6f, // v e r - E r r o
	0x72, 0x35, 0x30, 0x31, 0x20, 0x4e, 0x6f, 0x74, // r 5 0 1 - N o t
	0x20, 0x49, 0x6d, 0x70, 0x6c, 0x65, 0x6d, 0x65, // - I m p l e m e
	0x6e, 0x74, 0x65, 0x64, 0x35, 0x30, 0x33, 0x20, // n t e d 5 0 3 -
	0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x20, // S e r v i c e -
	0x55, 0x6e, 0x61, 0x76, 0x61, 0x69, 0x6c, 0x61, // U n a v a i l a
	0x62, 0x6c, 0x65, 0x4a, 0x61, 0x6e, 0x20, 0x46, // b l e J a n - F
	0x65, 0x62, 0x20, 0x4d, 0x61, 0x72, 0x20, 0x41, // e b - M a r - A
	0x70, 0x72, 0x20, 0x4d, 0x61, 0x79, 0x20, 0x4a, // p r - M a y - J
	0x75, 0x6e, 0x20, 0x4a, 0x75, 0x6c, 0x20, 0x41, // u n - J u l - A
	0x75, 0x67, 0x20, 0x53, 0x65, 0x70, 0x74, 0x20, // u g - S e p t -
	0x4f, 0x63, 0x74, 0x20, 0x4e, 0x6f, 0x76, 0x20, // O c t - N o v -
	0x44, 0x65, 0x63, 0x20, 0x30, 0x30, 0x3a, 0x30, // D e c - 0 0 - 0
	0x30, 0x3a, 0x30, 0x30, 0x20, 0x4d, 0x6f, 0x6e, // 0 - 0 0 - M o n
	0x2c, 0x20, 0x54, 0x75, 0x65, 0x2c, 0x20, 0x57, // - - T u e - - W
	0x65, 0x64, 0x2c, 0x20, 0x54, 0x68, 0x75, 0x2c, // e d - - T h u -
	0x20, 0x46, 0x72, 0x69, 0x2c, 0x20, 0x53, 0x61, // - F r i - - S a
	0x74, 0x2c, 0x20, 0x53, 0x75, 0x6e, 0x2c, 0x20, // t - - S u n - -
	0x47, 0x4d, 0x54, 0x63, 0x68, 0x75, 0x6e, 0x6b, // G M T c h u n k
	0x65, 0x64, 0x2c, 0x74, 0x65, 0x78, 0x74, 0x2f, // e d - t e x t -
	0x68, 0x74, 0x6d, 0x6c, 0x2c, 0x69, 0x6d, 0x61, // h t m l - i m a
	0x67, 0x65, 0x2f, 0x70, 0x6e, 0x67, 0x2c, 0x69, // g e - p n g - i
	0x6d, 0x61, 0x67, 0x65, 0x2f, 0x6a, 0x70, 0x67, // m a g e - j p g
	0x2c, 0x69, 0x6d, 0x61, 0x67, 0x65, 0x2f, 0x67, // - i m a g e - g
	0x69, 0x66, 0x2c, 0x61, 0x70, 0x70, 0x6c, 0x69, // i f - a p p l i
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x2f, 0x78, // c a t i o n - x
	0x6d, 0x6c, 0x2c, 0x61, 0x70, 0x70, 0x6c, 0x69, // m l - a p p l i
	0x63, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x2f, 0x78, // c a t i o n - x
	0x68, 0x74, 0x6d, 0x6c, 0x2b, 0x78, 0x6d, 0x6c, // h t m l - x m l
	0x2c, 0x74, 0x65, 0x78, 0x74, 0x2f, 0x70, 0x6c, // - t e x t - p l
	0x61, 0x69, 0x6e, 0x2c, 0x74, 0x65, 0x78, 0x74, // a i n - t e x t
	0x2f, 0x6a, 0x61, 0x76, 0x61, 0x73, 0x63, 0x72, // - j a v a s c r
	0x69, 0x70, 0x74, 0x2c, 0x70, 0x75, 0x62, 0x6c, // i p t - p u b l
	0x69, 0x63, 0x70, 0x72, 0x69, 0x76, 0x61, 0x74, // i c p r i v a t
	0x65, 0x6d, 0x61, 0x78, 0x2d, 0x61, 0x67, 0x65, // e m a x - a g e
	0x3d, 0x67, 0x7a, 0x69, 0x70, 0x2c, 0x64, 0x65, // - g z i p - d e
	0x66, 0x6c, 0x61, 0x74, 0x65, 0x2c, 0x73, 0x64, // f l a t e - s d
	0x63, 0x68, 0x63, 0x68, 0x61, 0x72, 0x73, 0x65, // c h c h a r s e
	0x74, 0x3d, 0x75, 0x74, 0x66, 0x2d, 0x38, 0x63, // t - u t f - 8 c
	0x68, 0x61, 0x72, 0x73, 0x65, 0x74, 0x3d, 0x69, // h a r s e t - i
	0x73, 0x6f, 0x2d, 0x38, 0x38, 0x35, 0x39, 0x2d, // s o - 8 8 5 9 -
	0x31, 0x2c, 0x75, 0x74, 0x66, 0x2d, 0x2c, 0x2a, // 1 - u t f - - -
	0x2c, 0x65, 0x6e, 0x71, 0x3d, 0x30, 0x2e, // - e n q - 0 -
}
