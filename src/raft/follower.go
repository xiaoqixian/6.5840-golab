// Date:   Tue Apr 16 11:19:43 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
	"sync"
	"time"
)

type LeaderInfo struct {
	id int
	term int
}

type VoteInfo struct {
	term int
	id int
}

type Follower struct {
	heartBeatTimer *time.Timer
	leaderInfo *LeaderInfo
	lastLogIndex int

	rf *Raft

	voteTo *VoteInfo
	voteLock sync.Mutex
}
func (*Follower) role() RoleEnum { return FOLLOWER }

// every node start as a Follower.
func (flw *Follower) init() {
	flw.setHeartBeatTimer()
}
func (*Follower) finish() {}

func (*Follower) name() string {
	return "Follower"
}

func followerFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	// TODO: need more work
	return &Follower {
		rf: cd.rf,
		leaderInfo: &LeaderInfo {
			id: -1,
			term: int(cd.term.Load()),
		},
	}
}

func followerFromLeader(r Role) Role {
	ld := r.(*Leader)
	return &Follower {
		rf: ld.rf,
		leaderInfo: &LeaderInfo {
			id: -1,
			term: ld.retireInfo.term,
		},
	}
}

func (flw *Follower) setHeartBeatTimer() {
	d := genRandomDuration(HEARTBEAT_TIMEOUT...)
	flw.log("Heartbeat timeout duration: %s", d)
	if flw.heartBeatTimer == nil || 
		!flw.heartBeatTimer.Reset(d) {
		flw.heartBeatTimer = time.AfterFunc(d, func() {
			flw.log("Heartbeat timeout")
			flw.rf.transRole(candidateFromFollower)
		})
	}
}

func (flw *Follower) resetHeartBeatTimer(rg ...int) {
	d := genRandomDuration(rg...)
	if flw.heartBeatTimer != nil && flw.heartBeatTimer.Reset(d) {
		flw.log("Reset Heartbeat timeout duration: %s", d)
	}
}

// RequestVote can cause the heartBeatTimer reset.
func (flw *Follower) requestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	flw.log("Receive RequestVote from %d", args.CandidateID)

	flw.voteLock.Lock()
	defer flw.voteLock.Unlock()

	rf := flw.rf
	voteTo := flw.voteTo

	reply.VoterID = rf.me

	if voteTo == nil || voteTo.term < args.Term {
		if args.LastLogIndex >= rf.logs.lastLogIndex() {
			voteTo = &VoteInfo {
				term: args.Term,
				id: args.CandidateID,
			}
			reply.VoteStatus = VOTE_GRANTED

			flw.resetHeartBeatTimer(LEADER_INIT_HEARTBEAT_TIMEOUT...)

			flw.log("Grant vote to %d", args.CandidateID)
		} else {
			reply.VoteStatus = VOTE_DENIAL
			reply.Term = flw.leaderInfo.term
			flw.log("Vote request from %d denialed cause of stale logs", args.CandidateID)
		}
	} else {
		reply.VoteStatus = VOTE_OTHER
		reply.Term = voteTo.term
		flw.log("Reject %d, already voted to %d", args.CandidateID, voteTo.id)
	}

	flw.voteTo = voteTo
}

func (flw *Follower) appendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	flw.log("Receive AppendEntries request from [%d:%d]", args.Id, args.Term)

	flw.resetHeartBeatTimer(HEARTBEAT_TIMEOUT...)

	// leader changed
	if args.Term > flw.leaderInfo.term {
		flw.updateLeader(args)
	}
	
	// TODO: log replication
}

func (flw *Follower) updateLeader(args *AppendEntriesArgs) {
	flw.leaderInfo.term = args.Term
	flw.leaderInfo.id = args.Id
}

func (flw *Follower) log(format string, args ...interface{}) {
	log.Printf("[Follower %d/%d] %s\n", flw.rf.me, flw.leaderInfo.term, fmt.Sprintf(format, args...))
}
