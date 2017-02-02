// Copyright 2017 Vallimamod Abdullah. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package esl

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
)

type MIMEMap struct {
	Map       textproto.MIMEHeader
	IsEscaped bool
}

type Event struct {
	UId     string
	Name    EventName
	App     string
	AppData string
	Stamp   int
	Type    EventType
	Header  MIMEMap
	Body    MIMEMap
	RawBody []byte
}

type EventType int

const (
	EventError EventType = iota
	EventState
	EventConnect
	EventAuth
	EventCommandReply
	EventApiResponse
	EventDisconnect
	EventGeneric
)

//go:generate enumer -type=EventName
type EventName int

const (
	CUSTOM EventName = iota
	CLONE
	CHANNEL_CREATE
	CHANNEL_DESTROY
	CHANNEL_STATE
	CHANNEL_CALLSTATE
	CHANNEL_ANSWER
	CHANNEL_HANGUP
	CHANNEL_HANGUP_COMPLETE
	CHANNEL_EXECUTE
	CHANNEL_EXECUTE_COMPLETE
	CHANNEL_HOLD
	CHANNEL_UNHOLD
	CHANNEL_BRIDGE
	CHANNEL_UNBRIDGE
	CHANNEL_PROGRESS
	CHANNEL_PROGRESS_MEDIA
	CHANNEL_OUTGOING
	CHANNEL_PARK
	CHANNEL_UNPARK
	CHANNEL_APPLICATION
	CHANNEL_ORIGINATE
	CHANNEL_UUID
	API
	LOG
	INBOUND_CHAN
	OUTBOUND_CHAN
	STARTUP
	SHUTDOWN
	PUBLISH
	UNPUBLISH
	TALK
	NOTALK
	SESSION_CRASH
	MODULE_LOAD
	MODULE_UNLOAD
	DTMF
	MESSAGE
	PRESENCE_IN
	NOTIFY_IN
	PRESENCE_OUT
	PRESENCE_PROBE
	MESSAGE_WAITING
	MESSAGE_QUERY
	ROSTER
	CODEC
	BACKGROUND_JOB
	DETECTED_SPEECH
	DETECTED_TONE
	PRIVATE_COMMAND
	HEARTBEAT
	TRAP
	ADD_SCHEDULE
	DEL_SCHEDULE
	EXE_SCHEDULE
	RE_SCHEDULE
	RELOADXML
	NOTIFY
	PHONE_FEATURE
	PHONE_FEATURE_SUBSCRIBE
	SEND_MESSAGE
	RECV_MESSAGE
	REQUEST_PARAMS
	CHANNEL_DATA
	GENERAL
	COMMAND
	SESSION_HEARTBEAT
	CLIENT_DISCONNECTED
	SERVER_DISCONNECTED
	SEND_INFO
	RECV_INFO
	RECV_RTCP_MESSAGE
	CALL_SECURE
	NAT
	RECORD_START
	RECORD_STOP
	PLAYBACK_START
	PLAYBACK_STOP
	CALL_UPDATE
	FAILURE
	SOCKET_DATA
	MEDIA_BUG_START
	MEDIA_BUG_STOP
	CONFERENCE_DATA_QUERY
	CONFERENCE_DATA
	CALL_SETUP_REQ
	CALL_SETUP_RESULT
	CALL_DETAIL
	DEVICE_STATE
	ALL
)

func NewEventFromReader(r *bufio.Reader) (*Event, error) {
	var err error
	e := &Event{}

	e.Header.Map, err = textproto.NewReader(r).ReadMIMEHeader()
	if err == io.EOF {
		// connection closed
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("parse headers: %v", err)
	}

	if slen := e.Get("Content-Length"); slen != "" {
		len, err := strconv.Atoi(slen)
		if err != nil {
			return nil, fmt.Errorf("convert content-length %s: %v", slen, err)
		}
		e.RawBody = make([]byte, len)
		_, err = io.ReadFull(r, e.RawBody)
		if err != nil {
			return nil, fmt.Errorf("read body: %v", err)
		}
	}

	switch e.Get("Content-Type") {
	case "auth/request":
		e.Type = EventAuth
	case "command/reply":
		e.Type = EventCommandReply
		reply := e.Get("Reply-Text")
		if !strings.Contains(reply, "+OK") && !strings.Contains(reply, "%2BOK") {
			return nil, fmt.Errorf("command error: %s", strings.TrimSpace(reply))
		}
		if strings.Contains(reply, "%") {
			e.Header.IsEscaped = true
		}
	case "text/event-plain":
		e.Type = EventGeneric
		err = e.parseTextBody()
	case "text/event-json", "text/event-xml":
		err = fmt.Errorf("unsupported format %s", e.Get("Content-Type"))
	case "text/disconnect-notice", "text/rude-rejection":
		e.Type = EventDisconnect
	case "api/response":
		e.Type = EventApiResponse
	}
	return e, err
}

func (e *Event) GetTextBody() string {
	slen := e.Body.Get("Content-Length")
	if slen != "" {
		bblen, err := strconv.Atoi(slen)
		if err != nil {
			log.Println("ERR: convert body len %s: %v", slen, err)
			return ""
		}
		blen := len(e.RawBody)
		return string(e.RawBody[blen-bblen : blen-1])
	}
	return ""
}

// Get retrieves the value of header from Event header or (if not found) from Event body.
// The value is returned unescaped and is empty if not found anywhere.
func (e Event) Get(header string) string {
	val := e.Header.Get(header)
	if val == "" {
		val = e.Body.Get(header)
	}
	return val
}

func (e Event) String() string {
	body, _ := url.QueryUnescape(string(e.RawBody))
	return fmt.Sprintf("%s\n.\n%s====================\n", e.Header, body)
}

func (m MIMEMap) Get(key string) string {
	val := m.Map.Get(key)
	if m.IsEscaped {
		val, _ = url.QueryUnescape(val)
	}
	return val
}

func (m MIMEMap) String() string {
	var s string
	for k, v := range m.Map {
		val := strings.Join(v, ",")
		if m.IsEscaped {
			val, _ = url.QueryUnescape(val)
		}
		s += fmt.Sprintf("%s: %s\n", k, val)
	}
	return s[:len(s)-1] // remove final newline
}

func (e *Event) parseTextBody() error {
	var err error
	buf := bufio.NewReader(bytes.NewReader(e.RawBody))
	if e.Body.Map, err = textproto.NewReader(buf).ReadMIMEHeader(); err != nil {
		return fmt.Errorf("parse text body: %v", err)
	}
	e.Body.IsEscaped = true
	e.UId = e.Get("Unique-ID")
	e.Name, _ = EventNameString(e.Get("Event-Name"))
	e.App = e.Get("Application")
	e.AppData = strings.TrimSpace(e.Get("Application-Data"))
	e.Stamp, err = strconv.Atoi(e.Get("Event-Date-Timestamp"))
	return err
}
