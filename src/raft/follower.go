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

func (flw *Follower) kill() {
	flw.log("Killed")
}

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
		if args.LastLogIndex >= int(rf.logs.lastCommitedIndex.Load()) &&
			args.Term > flw.leaderInfo.term {
			voteTo = &VoteInfo {
				term: args.Term,
				id: args.CandidateID,
			}
			reply.VoteStatus = VOTE_GRANTED

			flw.resetHeartBeatTimer(LEADER_INIT_HEARTBEAT_TIMEOUT...)
			flw.updateLeader(args.CandidateID, args.Term)

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

func (flw *Follower) leaderValid(args *AppendEntriesArgs, reply *AppendEntriesReply) bool {
	if args.Term < flw.leaderInfo.term {
		reply.Term = flw.leaderInfo.term
		reply.Success = false
		return false
	}
	return true
}

func (flw *Follower) appendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {

	if !flw.leaderValid(args, reply) { return }

	flw.resetHeartBeatTimer(HEARTBEAT_TIMEOUT...)

	// leader changed
	if args.Term > flw.leaderInfo.term {
		flw.updateLeader(args.Id, args.Term)
	}

	// heartBeat message
	// if len(args.Entries) == 0 { return }
	
	rf := flw.rf

	// heartBeat message
	if args.Entries == nil {
		flw.log("Receive HeartBeat from [%d/%d], LeaderCommit = %d", args.Id, args.Term, args.LeaderCommit)

		rf.logs.lastCommitedIndex.Store(int32(minInt(rf.logs.lastLogIndex(), args.LeaderCommit)))
		flw.log("HeartBeat: Update last commit index to %d", rf.logs.lastCommitedIndex.Load())
		go rf.applyLogs()
		
		return
	}

	flw.log("Receive AppendEntries request from [%d/%d], prev log info [%d/%d], LeaderCommit = %d.", args.Id, args.Term, args.PrevLogIndex, args.PrevLogTerm, args.LeaderCommit)

	// Update LeaderCommit in advance, so the prev log index can match.
	rf.logs.updateCommitIndex(args.LeaderCommit)
	flw.log("Before: Update last commit index to %d", rf.logs.lastCommitedIndex.Load())

	lastCommitedIndex, lastCommitedTerm := rf.logs.lastCommitedLogInfo()

	if lastCommitedIndex > args.PrevLogIndex {

		flwPrevLogTerm := rf.logs.indexLogTerm(args.PrevLogIndex)
		reply.Success = flwPrevLogTerm == args.PrevLogTerm
		flw.log("Receive leader old entry [%d/%d], flwPrevLogTerm = %d", args.PrevLogIndex, args.PrevLogTerm, flwPrevLogTerm)

	} else if lastCommitedIndex == args.PrevLogIndex && 
	lastCommitedTerm == args.PrevLogTerm {

		reply.Success = true
		rf.logs.appendEntry(args.Entries)
		rf.logs.updateCommitIndex(args.LeaderCommit)
		flw.log("After: Update last commit index to %d", rf.logs.lastCommitedIndex.Load())

		go rf.applyLogs()

	} else {
		reply.Success = false
		flw.log("Leader [%d/%d] does not match follower [%d/%d]", args.PrevLogIndex, args.PrevLogTerm, lastCommitedIndex, lastCommitedTerm)
	}

}

func (flw *Follower) updateLeader(id int, term int) {
	flw.log("Update leader to [%d/%d]", id, term)
	flw.leaderInfo.id = id
	flw.leaderInfo.term = term
}

func (flw *Follower) log(format string, args ...interface{}) {
	log.Printf("[Follower %d/%d/%d] %s\n", flw.rf.me, flw.leaderInfo.term, flw.rf.logs.lastCommitedIndex.Load(), fmt.Sprintf(format, args...))
}
