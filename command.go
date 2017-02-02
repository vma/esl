// Copyright 2017 Vallimamod Abdullah. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package esl

import (
	"bytes"
	"fmt"
)

type Command struct {
	Sync bool
	UId  string
	App  string
	Args string
}

// Serialize formats (serializes) the command as expected by freeswitch.
func (cmd *Command) Serialize() []byte {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("sendmsg %s\ncall-command: execute\n", cmd.UId))
	buf.WriteString(fmt.Sprintf("execute-app-name: %s\nexecute-app-arg: %s\n", cmd.App, cmd.Args))
	if cmd.Sync {
		buf.WriteString("event-lock: true\n")
	} else {
		buf.WriteString("event-lock: false\n")
	}
	buf.WriteString("\n\n")
	return buf.Bytes()
}

// Execute sends Command cmd over Connection and waits for reply.
// Returns the command reply event pointer or an error if any.
func (cmd Command) Execute(con *Connection) (*Event, error) {
	_, err := con.Write(cmd.Serialize())
	if err != nil {
		return nil, fmt.Errorf("execute command: con write: %v", err)
	}
	ev := <-con.cmdReply
	return ev, nil
}
