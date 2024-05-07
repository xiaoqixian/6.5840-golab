// Date:   Tue Apr 16 11:42:29 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"log"
	"math/rand"
	"reflect"
	"runtime"
	"time"
)

const (
	HEARTBEAT_SEND = time.Duration(50 * time.Millisecond)
	RPC_FAIL_WAITING = time.Duration(100 * time.Millisecond)
	RPC_CALL_TRY_TIMES = 5
	NEW_LOG_CHECK_FREQ = time.Duration(20 * time.Millisecond)
	CANDIDATE_CHECK_FREQ = time.Duration(10 * time.Millisecond)
	HOLD_WAITING = time.Duration(10 * time.Millisecond)
)
var (
	HEARTBEAT_TIMEOUT = []int{ 300, 400 }
	POLL_TIMEOUT_DURATION = []int{ 150, 300 }
	ELECTION_TIMEOUT = []int{ 900, 1100 }
	// heartbeat timeout should be longer thant normal heartbeat timeout.
	LEADER_INIT_HEARTBEAT_TIMEOUT = []int{ 500, 800 }
)

type RpcReq interface {
	reqType()
}

type AppendEntriesReq struct {
	args *AppendEntriesArgs
	reply *AppendEntriesReply
	finishCh chan bool
}

type RequestVoteReq struct {
	args *RequestVoteArgs
	reply *RequestVoteReply
	finishCh chan bool
}

type EntryStatus uint8 
const (
	// not used, to avoid EntryStatus not set.
	ENTRY_DEFAULT EntryStatus = iota

	// Entry is successfully received by the follower.
	ENTRY_SUCCESS

	// Entry does not match follower log entries.
	ENTRY_FAILURE

	// Entry is received by a candidate or a leader, 
	// wait a small amount of time and resend request 
	// with the same log index.
	ENTRY_HOLD

	// Entry is received by a candidate or a leader with 
	// greater term, the leader that received this should 
	// retire immediately.
	ENTRY_STALE
)

func (*AppendEntriesReq) reqType() {}
func (*RequestVoteReq) reqType() {}

func genRandomDuration(t ...int) time.Duration {
	switch len(t) {
	case 1:
		return time.Duration(rand.Intn(t[0])) * time.Millisecond
	case 2:
		return time.Duration(t[0] + rand.Intn(t[1] - t[0])) * time.Millisecond

	default:
		log.Fatalf("genRandomDuration: invalid parameter list: %v", t)
		panic("")
	}
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

func minInt(x int, y int) int {
	if x <= y { return x }
	return y
}

func maxInt(x int, y int) int {
	if x >= y { return x }
	return y
}

func typeName(a interface{}) string {
	return reflect.TypeOf(a).String()
}

func rpcMultiTry(f func() bool) (ok bool) {
	ticker := time.NewTicker(RPC_FAIL_WAITING)
	ch := make(chan bool, 1)

	loop: for {
		go func() {
			ch <- f()
		}()

		select {
		case ok = <- ch:
			break loop
		case <- ticker.C:
		}
	}
	return
}
