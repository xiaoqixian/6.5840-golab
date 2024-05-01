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
	if flw.active.CompareAndSwap(false, true) {
		flw.active.Store(true)
		flw.rf.chLock.Unlock()
		flw.tickHeartBeatTimer()
	}
}

func (flw *Follower) stop() bool {
	if flw.active.CompareAndSwap(true, false) {
		if flw.hbTimer != nil {
			flw.hbTimer.Stop()
		}
		flw.rf.chLock.Lock()
		return true
	} else {
		return false
	}
}

func (flw *Follower) process(ev Event) {
	switch ev := ev.(type) {
	case *GetStateEvent:
		ev.ch <- &NodeState {
			term: flw.term,
			isLeader: false,
		}

	case *StartCommandEvent:
		ev.ch <- &StartCommandReply { false, -1, -1 }

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
	flw.log("AppendEntries RPC from [%d/%d], PrevLogInfo = [%d/%d], LeaderCommit = %d", args.Id, args.Term, args.PrevLogInfo.Index, args.PrevLogInfo.Term, args.LeaderCommit)
	
	if args.Term < flw.term {
		reply.EntryStatus = ENTRY_STALE
		reply.Term = flw.term
		return
	}

	flw.term = args.Term
	flw.tickHeartBeatTimer()

	ok := flw.rf.logs.followerAppendEntry(args.Entry, args.PrevLogInfo)

	if ok {
		flw.rf.logs.updateCommit(minInt(args.LeaderCommit, args.PrevLogInfo.Index))
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
	flw.log("RequestVote RPC from [%d/%d], lli = [%d/%d]", args.CandidateID, args.Term, args.LastLogInfo.Index, args.LastLogInfo.Term)

	reply.VoterID = flw.rf.me
	switch {
	case args.Term < flw.term:
		reply.Term = flw.term
		reply.VoteStatus = VOTE_DENIAL

	case args.Term == flw.term:
		reply.VoteStatus = VOTE_OTHER

	case args.Term > flw.term:
		flw.term = args.Term
		if flw.rf.logs.atLeastUpToDate(args.LastLogInfo) {
			flw.log("Grant vote to %d", args.CandidateID)
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

func (flw *Follower) _log(f func(string, ...interface{}), format string, args ...interface{}) {
	f("[Follower %d/%d/%d/%d] %s", flw.rf.me, flw.term, flw.rf.logs.LLI(), flw.rf.logs.LCI(), fmt.Sprintf(format, args...))
}

func (flw *Follower) log(format string, args ...interface{}) {
	flw._log(log.Printf, format, args...)
}
func (flw *Follower) fatal(format string, args ...interface{}) {
	flw._log(log.Fatalf, format, args...)
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
