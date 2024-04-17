// Date:   Tue Apr 16 11:42:29 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"log"
	"math/rand"
	"time"
)

const (
	HEARTBEAT_SEND = time.Duration(time.Second)
	ELECTION_WAITING = 100
	RPC_CALL_TRY_TIMES = 5
)
var (
	HEARTBEAT_TIMEOUT = []int{ 150, 300 }
	POLL_TIMEOUT_DURATION = []int{ 150, 300 }
)

func genRandomDuration(t ...int) time.Duration {
	switch len(t) {
	case 1:
		return time.Duration(rand.Intn(t[0])) * time.Millisecond
	case 2:
		return time.Duration(t[0] + rand.Intn(t[0] - t[1])) * time.Millisecond

	default:
		log.Fatalf("genRandomDuration: invalid parameter list: %v", t)
		panic("")
	}
}
