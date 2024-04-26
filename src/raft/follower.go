// Date:   Tue Apr 16 11:19:43 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
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

	term int

	rf *Raft

	killed atomic.Bool

	voteTo *VoteInfo
	voteLock sync.Mutex

	reqChan chan RpcReq

	sync.RWMutex
}
func (*Follower) role() RoleEnum { return FOLLOWER }

// every node start as a Follower.
func (flw *Follower) init() {
	flw.setHeartBeatTimer()
}
func (flw *Follower) finish() {
	flw.Lock()
}

func (flw *Follower) kill() {
	flw.log("Killed")
	flw.killed.Store(true)
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
		reqChan: make(chan RpcReq),
	}
}

func followerFromLeader(r Role) Role {
	ld := r.(*Leader)
	return &Follower {
		rf: ld.rf,
		term: ld.term,
		reqChan: make(chan RpcReq),
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

func (flw *Follower) requestVote(req *RequestVoteReq) {
	if !flw.TryRLock() { 
		req.finishCh <- true
		return 
	}

	defer func() {
		req.finishCh <- true
		flw.RUnlock()
	}()

	args, reply := req.args, req.reply
	rf, voteTo := flw.rf, flw.voteTo
	reply.VoterID = rf.me
	logs := rf.logs

	if voteTo == nil || voteTo.term < args.Term {
		if args.LastLogIndex >= int(logs.lastCommitedIndex.Load()) &&
		args.Term > flw.term {
			voteTo = &VoteInfo {
				term: args.Term,
			}
			reply.VoteStatus = VOTE_GRANTED

			flw.resetHeartBeatTimer(LEADER_INIT_HEARTBEAT_TIMEOUT...)
			flw.term = args.Term
			flw.log("Grant vote to %d", args.CandidateID)
		} else {
			reply.VoteStatus = VOTE_OTHER
			reply.Term = flw.term
			flw.log("Vote request from %d rejected cause of stale logs", args.CandidateID)
		}
	} else {
		reply.VoteStatus = VOTE_OTHER
		reply.Term = voteTo.term
		flw.log("Reject %d, already voted to %d", args.CandidateID, voteTo.id)
	}

	flw.voteTo = voteTo
}

func (flw *Follower) appendEntries(req *AppendEntriesReq) {
	if !flw.TryRLock() { 
		req.finishCh <- true
		return 
	}

	defer func() {
		req.finishCh <- true
		flw.RUnlock()
	}()

	args, reply := req.args, req.reply
	rf := flw.rf
	reply.Term = args.Term

	// reject stale leaders.
	if args.Term < flw.term {
		reply.Term = flw.term
		reply.EntryStatus = ENTRY_STALE
		return
	}

	flw.resetHeartBeatTimer(HEARTBEAT_TIMEOUT...)

	// update term and last commited index.
	flw.term = args.Term
	rf.logs.updateCommitIndex(args.LeaderCommit)

	// heartBeat message
	if args.Entries == nil {
		flw.log("Receive HeartBeat from [%d/%d], LeaderCommit = %d", args.Id, args.Term, args.LeaderCommit)

		flw.log("HeartBeat: Update last commit index to %d", rf.logs.lastCommitedIndex.Load())
		go rf.applyLogs()
		
		return
	}

	
	flw.log("Receive AppendEntries request from [%d/%d], prev log info [%d/%d], LeaderCommit = %d.", args.Id, args.Term, args.PrevLogIndex, args.PrevLogTerm, args.LeaderCommit)

	reply.EntryStatus = rf.logs.followerAppendEntry(args)
}

func (flw *Follower) updateLeader(id int, term int) {
	flw.log("Update leader to [%d/%d]", id, term)
	flw.leaderInfo.id = id
	flw.leaderInfo.term = term
}

func (flw *Follower) log(format string, args ...interface{}) {
	log.Printf("[Follower %d/%d/%d] %s\n", flw.rf.me, flw.leaderInfo.term, flw.rf.logs.lastCommitedIndex.Load(), fmt.Sprintf(format, args...))
}

func (flw *Follower) fatal(format string, args ...interface{}) {
	log.Fatalf("[Follower %d/%d/%d] %s\n", flw.rf.me, flw.leaderInfo.term, flw.rf.logs.lastCommitedIndex.Load(), fmt.Sprintf(format, args...))
}
