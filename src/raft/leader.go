// Date:   Wed Apr 17 09:40:48 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"

	"6.5840/labrpc"
)

// information for a leader transform to a follower.
type RetireInfo struct {
	term int
}

type Leader struct {
	term int

	rf *Raft

	heartBeatTimer *RepeatTimer

	retireInfo *RetireInfo
}

func leaderFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	return &Leader {
		term: int(cd.term.Load()),
		rf: cd.rf,
	}
}

func (*Leader) role() RoleEnum { return LEADER }

func (ld *Leader) init() {
	ld.setHeartbeatTimer()
}
func (ld *Leader) finish() {
	if ld.heartBeatTimer != nil {
		ld.heartBeatTimer.kill()
	}
}

func (*Leader) name() string {
	return "Leader"
}

// TODO: should a stale leader vote
func (ld *Leader) requestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	ld.log("RequestVote from %d with term %d", args.CandidateID, args.Term)
	rf := ld.rf
	reply.VoterID = rf.me

	if args.Term > ld.term {
		// leader expired
		reply.VoteStatus = VOTE_GRANTED
		ld.log("Retire cause of stale term")
		go ld.retire()
	} else {
		reply.VoteStatus = VOTE_DENIAL
		reply.Term = ld.term
	}
}

// An AppendEntries RPC call with a greater term will triger the 
// transformation from Leader to Follwer.
func (ld *Leader) appendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	ld.log("AppendEntries request from [%d, %d]", args.Id, args.Term)
	if args.Term > ld.term {
		go ld.retire()
	} else {
		ld.log("%d is a stale leader", args.Id)
	}
}

func (ld *Leader) retire() {
	ld.log("Retire")
	ld.retireInfo = &RetireInfo {
		term: ld.term,
	}
	ld.rf.transRole(followerFromLeader)
}

func (ld *Leader) sendHeartBeats() {
	rf := ld.rf
	args := &AppendEntriesArgs {
		Id: rf.me,
		Term: ld.term,
	}
	reply := &AppendEntriesReply {}

	for i, peer := range rf.peers {
		if i == rf.me { continue }
		go func(peer *labrpc.ClientEnd, id int) {
			ld.log("Send heartbeat to %d", id)
			ok, tries := false, RPC_CALL_TRY_TIMES
			for !ok && tries > 0 {
				tries--
				ok = peer.Call("Raft.AppendEntries", args, reply)
			}
			// TODO: process AppendEntriesReply
		}(peer, i)
	}
}

func (ld *Leader) setHeartbeatTimer() {
	if ld.heartBeatTimer == nil {
		ld.heartBeatTimer = newRepeatTimer(HEARTBEAT_SEND, ld.sendHeartBeats)
	} else {
		ld.heartBeatTimer.reset(HEARTBEAT_SEND)
	}
}

func (ld *Leader) log(format string, args ...interface{}) {
	log.Printf("[Leader %d/%d] %s\n", ld.rf.me, ld.term, fmt.Sprintf(format, args...))
}
