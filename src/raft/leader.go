// Date:   Wed Apr 17 09:40:48 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"6.5840/labrpc"
)

type LeaderEvent interface {
	callback(*Leader, interface{}) interface{}
}

type Nothing struct {}
type SendHeartBeatEvent struct {}

func (*SendHeartBeatEvent) callback(ld *Leader, args interface{}) interface{} {
	rf := ld.rf
	hbArgs := &AppendEntriesArgs {
		Id: rf.me,
		Term: ld.term,
		LeaderCommit: int(rf.logs.lastCommitedIndex.Load()),
	}
	for i, peer := range rf.peers {
		go func(peer *labrpc.ClientEnd, id int) {
			reply := &AppendEntriesReply {}
			ok := peer.Call("Raft.AppendEntries", hbArgs, reply)
			tries := RPC_CALL_TRY_TIMES
			
			for (!ok || !reply.Responsed) && tries > 0 {
				tries--
				ok = peer.Call("Raft.AppendEntries", args, reply)
			}
		}(peer, i)
	}
	return Nothing{}
}

type Leader struct {
	term int

	nextIndex []int

	rf *Raft

	evCh chan LeaderEvent

	heartBeatTimer *RepeatTimer
}

type ReplyCallback func(*Leader, interface{})

func leaderFromCandidate(role Role) Role {
	cd := role.(*Candidate)
	rf := cd.rf
	nextIndex := make([]int, len(rf.peers))
	lci := int(rf.logs.lastCommitedIndex.Load())
	for i := 0; i < len(rf.peers); i++ {
		nextIndex[i] = lci
	}

	return &Leader {
		term: int(cd.term.Load()),
		nextIndex: nextIndex,
		rf: rf,
	}
}

func (ld *Leader) init() {
	ld.setHeartBeatTimer()
}

func (ld *Leader) finish() {

}

func (ld *Leader) kill() {}

func (*Leader) role() RoleEnum { return LEADER }
func (*Leader) name() string { return "Leader" }

func (ld *Leader) requestVote(req *RequestVoteReq) {
	
}
func (ld *Leader) appendEntries(req *AppendEntriesReq) {}

func (ld *Leader) log(format string, args ...interface{}) {
	log.Printf("[Leader %d/%d] %s\n", ld.rf.me, ld.term, fmt.Sprintf(format, args...))
}
func (ld *Leader) fatal(format string, args ...interface{}) {
	log.Fatalf("[Leader %d/%d] %s\n", ld.rf.me, ld.term, fmt.Sprintf(format, args...))
}

func (ld *Leader) setHeartBeatTimer() {
	ld.heartBeatTimer = newRepeatTimer(HEARTBEAT_SEND, func() {
		ld.evCh <- &SendHeartBeatEvent{}
	})
}
