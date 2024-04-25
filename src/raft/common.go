// Date:   Tue Apr 16 11:42:29 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"log"
	"math/rand"
	"runtime"
	"time"
)

const (
	HEARTBEAT_SEND = time.Duration(50 * time.Millisecond)
	RPC_FAIL_WAITING = time.Duration(20 * time.Millisecond)
	RPC_CALL_TRY_TIMES = 5
	NEW_LOG_CHECK_FREQ = time.Duration(20 * time.Millisecond)
	CANDIDATE_CHECK_FREQ = time.Duration(10 * time.Millisecond)
)
var (
	HEARTBEAT_TIMEOUT = []int{ 150, 300 }
	POLL_TIMEOUT_DURATION = []int{ 150, 300 }
	ELECTION_TIMEOUT_WAITING_DURATION = []int{ 100, 200 }
	// heartbeat timeout should be longer thant normal heartbeat timeout.
	LEADER_INIT_HEARTBEAT_TIMEOUT = []int{ 500, 800 }
)

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
