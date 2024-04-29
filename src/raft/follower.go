// Date:   Tue Apr 16 11:19:43 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
	"sync/atomic"
	"time"
)

type Follower struct {
	term int
	rf *Raft

	active atomic.Bool
	
	hbTimer *time.Timer
}

func (*Follower) role() RoleEnum { return FOLLOWER }

func (flw *Follower) closed() bool { return !flw.active.Load() }

func (flw *Follower) activate() { 
	flw.active.Store(true)
	flw.rf.chLock.Unlock()
	flw.tickHeartBeatTimer()
}

func (flw *Follower) stop() {
	flw.active.Store(false)
	flw.log("Stopping")
	flw.rf.chLock.Lock()
	flw.log("chLock locked")
	if flw.hbTimer != nil {
		flw.hbTimer.Stop()
	}
}

func (flw *Follower) process(ev Event) {
	flw.log("Process ev %s", typeName(ev))
	switch ev := ev.(type) {
	case *GetStateEvent:
		ev.ch <- &NodeState {
			term: flw.term,
			isLeader: false,
		}

	case *StartCommandEvent:
		ev.ch <- nil

	case *AppendEntriesEvent:
		flw.appendEntries(ev)

	case *RequestVoteEvent:
		flw.requestVote(ev)

	case *HeartBeatTimeoutEvent:
		flw.log("HeartBeat Timeout")
		flw.rf.transRole(candidateFromFollower)

	default:
		flw.fatal("Unknown event type: %s", typeName(ev))
	}
}

// Possible entry status replied:
// 1. ENTRY_STALE, if the leader is stale.
// 2. ENTRY_FAILUEE, if the prev log info cannot match
// 3. ENTRY_SUCCESS, if the entry is well received.
func (flw *Follower) appendEntries(ev *AppendEntriesEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	flw.log("AppendEntries RPC from [%d/%d]", args.Id, args.Term)
	
	if args.Term < flw.term {
		reply.EntryStatus = ENTRY_STALE
		reply.Term = flw.term
		return
	}

	flw.term = args.Term
	flw.rf.logs.updateCommit(args.LeaderCommit)
	flw.tickHeartBeatTimer()
	
	ok := flw.rf.logs.followerAppendEntry(
		args.Entry,
		PrevLogInfo { args.PrevLogIndex, args.PrevLogTerm },
	)

	if ok {
		reply.EntryStatus = ENTRY_SUCCESS
	} else {
		reply.EntryStatus = ENTRY_FAILURE
	}
}

// Possible requestVote reply:
// 1. VOTE_DENIAL, if the candidate term is less than the current term.
// 2. VOTE_OTHER, if the candidate term is at least as large as the current 
//    term, but the last commited log index is less than the current.
// 3. VOTE_GRANT, if the candidate has an at least as large term and 
//    at least as large last commited index.
func (flw *Follower) requestVote(ev *RequestVoteEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	flw.log("RequestVote RPC from [%d/%d]", args.CandidateID, args.Term)

	reply.VoterID = flw.rf.me
	switch {
	case args.Term < flw.term:
		reply.Term = flw.term
		reply.VoteStatus = VOTE_DENIAL

	case args.Term == flw.term:
		reply.VoteStatus = VOTE_OTHER

	case args.Term > flw.term:
		flw.term = args.Term
		if args.LastCommitedIndex >= flw.rf.logs.LCI() {
			reply.VoteStatus = VOTE_GRANTED
			flw.tickHeartBeatTimer()
		} else {
			reply.VoteStatus = VOTE_OTHER
		}
	}
}

func (flw *Follower) tickHeartBeatTimer() {
	d := genRandomDuration(HEARTBEAT_TIMEOUT...)
	flw.log("HeartBeat timeout after %s", d)
	if flw.hbTimer == nil || !flw.hbTimer.Reset(d) {
		flw.hbTimer = time.AfterFunc(d, func() {
			flw.rf.tryPutEv(&HeartBeatTimeoutEvent{}, flw)
		})
	}
}

func (flw *Follower) log(format string, args ...interface{}) {
	log.Printf("[Follower %d/%d] %s", flw.rf.me, flw.term, fmt.Sprintf(format, args...))
}
func (flw *Follower) fatal(format string, args ...interface{}) {
	log.Fatalf("[Follower %d/%d] %s", flw.rf.me, flw.term, fmt.Sprintf(format, args...))
}

func followerFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	return &Follower {
		term: cd.term,
		rf: cd.rf,
	}
}

func followerFromLeader(r Role) Role {
	ld := r.(*Leader)
	return &Follower {
		term: ld.term,
		rf: ld.rf,
	}
}
