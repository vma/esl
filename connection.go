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
	"net"
	"strings"
	"time"
)

type ConnectionHandler interface {
	OnConnect(con *Connection)
	OnEvent(con *Connection, ev *Event)
	OnDisconnect(con *Connection, ev *Event)
	OnClose(con *Connection)
}

type Connection struct {
	socket     net.Conn
	buffer     *bufio.ReadWriter
	cmdReply   chan *Event
	apiResp    chan *Event
	Handler    ConnectionHandler
	Address    string
	Password   string
	Connected  bool
	MaxRetries int
	Timeout    time.Duration
	UserData   interface{}
}

func NewConnection(host string, handler ConnectionHandler) (*Connection, error) {
	con := Connection{
		Address:  host,
		Password: "ClueCon",
		Timeout:  3 * time.Second,
		Handler:  handler,
	}
	con.cmdReply = make(chan *Event)
	con.apiResp = make(chan *Event)
	err := con.ConnectRetry(3)
	if err != nil {
		return nil, fmt.Errorf("connect: %v", err)
	}
	go con.Handler.OnConnect(&con)
	return &con, nil
}

func (con *Connection) SendRecv(cmd string, args ...string) (*Event, error) {
	buf := bytes.NewBufferString(cmd)
	for _, arg := range args {
		buf.WriteString(" ")
		buf.WriteString(arg)
	}
	buf.WriteString("\n\n")
	_, err := con.Write(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("send bytes: %v", err)
	}
	ev := <-con.cmdReply
	reply := ev.Get("Reply-Text")
	if strings.HasPrefix(reply, "-ERR") {
		return nil, fmt.Errorf("SendRecv %s %s: %s", cmd, args, strings.TrimSpace(reply))
	}
	return ev, nil
}

func (con *Connection) MustSendRecv(cmd string, args ...string) *Event {
	ev, err := con.SendRecv(cmd, args...)
	if err != nil {
		con.Close()
		log.Fatal("ERR: ", err)
	}
	return ev
}

func (con *Connection) SendEvent(cmd string, headers map[string]string, body []byte) (*Event, error) {
	buf := bytes.NewBufferString(fmt.Sprintf("sendevent %s\n", cmd))
	for k, v := range headers {
		buf.WriteString(fmt.Sprintf("%s: %s\n", k, v))
	}
	buf.WriteString(fmt.Sprintf("Content-Length: %d\n\n", len(body)))
	buf.Write(body)

	_, err := con.Write(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("send event: %v", err)
	}
	ev := <-con.cmdReply
	reply := ev.Get("Reply-Text")
	if strings.HasPrefix(reply, "-ERR") {
		return nil, fmt.Errorf("send event %s: %s", cmd, strings.TrimSpace(reply))
	}
	return ev, nil
}

func (con *Connection) Api(cmd string, args ...string) (string, error) {
	buf := bytes.NewBufferString("api " + cmd)
	for _, arg := range args {
		buf.WriteString(" ")
		buf.WriteString(arg)
	}
	buf.WriteString("\n\n")
	_, err := con.Write(buf.Bytes())
	if err != nil {
		return "", fmt.Errorf("send bytes: %v", err)
	}
	ev := <-con.apiResp
	resp := strings.TrimSpace(string(ev.RawBody))
	if strings.HasPrefix(resp, "-ERR") {
		return "", fmt.Errorf("api %s %s: %s", cmd, args, resp)
	}
	return string(ev.RawBody), nil
}

func (con *Connection) BgApi(cmd string, args ...string) (string, error) {
	repl, err := con.SendRecv("bgapi "+cmd, args...)
	if err != nil {
		return "", fmt.Errorf("bgapi: %v", err)
	}
	return repl.Get("Job-Uuid"), nil
}

func (con *Connection) Execute(app string, uuid string, params ...string) (*Event, error) {
	args := strings.Join(params, " ")
	cmd := Command{
		Sync: false,
		UId:  uuid,
		App:  app,
		Args: args,
	}
	return cmd.Execute(con)
}

func (con *Connection) ExecuteSync(app string, uuid string, params ...string) (*Event, error) {
	args := strings.Join(params, " ")
	cmd := Command{
		Sync: true,
		UId:  uuid,
		App:  app,
		Args: args,
	}
	return cmd.Execute(con)
}

func (con *Connection) ConnectRetry(MaxRetries int) error {
	for retries := 1; !con.Connected && retries <= MaxRetries; retries++ {
		c, err := net.DialTimeout("tcp", con.Address, con.Timeout)
		if err != nil {
			if retries == MaxRetries {
				return fmt.Errorf("last dial attempt: %v", err)
			}
			log.Printf("NOTICE: dial attempt #%d: %v, retrying\n", retries, err)
		} else {
			con.socket = c
			break
		}
	}
	con.buffer = bufio.NewReadWriter(bufio.NewReaderSize(con.socket, 16*1024),
		bufio.NewWriter(con.socket))
	return con.Authenticate()
}

// Authenticate handles freeswitch esl authentication
func (con *Connection) Authenticate() error {
	ev, err := NewEventFromReader(con.buffer.Reader)
	if err != nil || ev.Type != EventAuth {
		con.socket.Close()
		if ev.Type != EventAuth {
			return fmt.Errorf("bad auth preamble: [%s]", ev.Header)
		}
		return fmt.Errorf("socket read error: %v", err)
	}

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("auth %s\n\n", con.Password))
	if _, err := con.Write(buf.Bytes()); err != nil {
		con.socket.Close()
		return fmt.Errorf("passwd buffer flush: %v", err)
	}

	ev, err = NewEventFromReader(con.buffer.Reader)
	if err != nil {
		con.socket.Close()
		return fmt.Errorf("auth reply: %v", err)
	}
	if ev.Type != EventCommandReply {
		con.socket.Close()
		return fmt.Errorf("bad reply type: %#v", ev.Type)
	}
	con.Connected = true
	return nil
}

func (con *Connection) HandleEvents() error {
	for con.Connected {
		ev, err := NewEventFromReader(con.buffer.Reader)
		if err != nil {
			if err == io.EOF || !con.Connected {
				return nil
			}
			con.Close()
			return fmt.Errorf("event read loop: %v\n", err)
		}
		switch ev.Type {
		case EventError:
			return fmt.Errorf("invalid event: [%s]", ev)
		case EventDisconnect:
			con.Handler.OnDisconnect(con, ev)
		case EventCommandReply:
			con.cmdReply <- ev
		case EventApiResponse:
			con.apiResp <- ev
		case EventGeneric:
			go con.Handler.OnEvent(con, ev)
		}
	}
	return fmt.Errorf("disconnected")
}

func (con *Connection) Write(b []byte) (int, error) {
	defer con.buffer.Flush()
	return con.buffer.Write(b)
}

func (con *Connection) Close() {
	if con.Connected {
		con.Connected = false
		con.Handler.OnClose(con)
	}
	con.socket.Close()
}
