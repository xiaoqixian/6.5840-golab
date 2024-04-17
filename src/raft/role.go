// Date:   Tue Apr 16 11:19:43 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"time"
)

type LeaderInfo struct {
	id int
	term int
}

type Follower struct {
	voteFor *Candidate
	heartBeatTimer *time.Timer
	leaderInfo *LeaderInfo
	lastLogIndex int

	rf *Raft
}
func (*Follower) role() RoleEnum { return FOLLOWER }

func followerFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	// TODO: need more work
	return &Follower {
		rf: cd.rf,
		leaderInfo: &LeaderInfo {
			id: -1,
			term: cd.term,
		},
	}
}

func (flw *Follower) setTimer() {
	d := genRandomDuration(HEARTBEAT_TIMEOUT...)
	if flw.heartBeatTimer == nil || 
		!flw.heartBeatTimer.Reset(d) {
		flw.heartBeatTimer = time.AfterFunc(d, func() {
			flw.startElection()
		})
	}
}

func (flw *Follower) resetTimer() bool {
	return flw.heartBeatTimer != nil && 
		flw.heartBeatTimer.Reset(genRandomDuration(HEARTBEAT_TIMEOUT...))
}

// every node start as a Follower.
func (flw *Follower) init() {
	flw.setTimer()
}
