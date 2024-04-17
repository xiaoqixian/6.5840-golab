// Date:   Wed Apr 17 09:40:48 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import "6.5840/labrpc"

type Leader struct {
	term int

	rf *Raft

	heartBeatTimer *RepeatTimer
}

func leaderFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	return &Leader {
		term: cd.term,
		rf: cd.rf,
	}
}

func (*Leader) role() RoleEnum { return LEADER }

func (ld *Leader) init() {
	ld.setHeartbeatTimer()
}

func (ld *Leader) sendHeartBeats() {
	rf := ld.rf
	args := &AppendEntriesArgs {
		Id: rf.me,
		Term: ld.term,
	}
	reply := &AppendEntriesReply {}

	for i, peer := range ld.rf.peers {
		if i == ld.rf.me { continue }
		go func(peer *labrpc.ClientEnd) {
			peer.Call("Raft.AppendEntries", args, reply)
		}(peer)
	}
}

func (ld *Leader) setHeartbeatTimer() {
	if ld.heartBeatTimer == nil {
		ld.heartBeatTimer = newRepeatTimer(HEARTBEAT_SEND, ld.sendHeartBeats)
	} else {
		ld.heartBeatTimer.reset(HEARTBEAT_SEND)
	}
}
