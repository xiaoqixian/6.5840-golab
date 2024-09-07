// Date:   Wed May 15 13:38:20 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package kvraft

import (
	"log"
	"runtime"
	"time"
)

const (
	FIND_LEADER_WATING = time.Duration(300 * time.Millisecond)
)

const (
	CLERK_COMMAND_BUF_SZ int = 16
)

type OpResult uint8
const (
	// throw an error on default, forces 
	// the user to assign the Err field.
	OP_DEFAULT OpResult = iota 

	OP_SUCCESS

	// failed to commit.
	OP_FAIL

	// rpc ID not sync
	OP_UNSYNC
)

type RpcArgs struct {
	ClientID int
	RpcID int
	OpArgs interface {}
}

type RpcReply struct {
	OpRes OpResult
	RpcID int
	OpReply interface {}
}

// Put or Append
type PutAppendArgs struct {
	Key   string
	Value string
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}

type PutAppendReply struct {
	OpRes OpResult
}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.
}

type GetReply struct {
	OpRes OpResult
	Value string
}

type Op interface {
	// Your definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}

// represents a command started on 
// this server.
type CmdInfo struct {
	cmdIndex int
	clientID int
	rpcID int
	ch chan bool
	reply *RpcReply
	args *RpcArgs
}
// the actual type transfered to Raft
type CmdEntry struct {
	// the ClientID and RpcID form a unique pair.
	clientID int
	rpcID int

	op Op
}

// The Get op does no harm, so peers will not actually 
// apply it. 
// The leader commits it only to make sure that it's still 
// legal.
type GetOp struct {}

type PutOp struct {
	Key string
	Value string
}
type AppendOp struct {
	Key string
	Value string
}

type GetStateReply struct {
	Term int
	IsLeader bool
}

func assert(pred bool) {
	if !pred {
		_, file, line, ok := runtime.Caller(1)
		if ok {
			log.Fatalf("[%s:%d] assertion failed\n", file, line)
		} else {
			log.Fatal("[Unknown] assertion failed\n")
		}
	}
}

