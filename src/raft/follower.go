// Date:   Tue Apr 16 11:19:43 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"sync/atomic"
	"time"
)

type Follower struct {
	rf *Raft

	active atomic.Bool
	
	hbTimer *time.Timer
}

func (*Follower) role() RoleEnum { return FOLLOWER }
func (*Follower) name() string { return "Follower" }

func (flw *Follower) closed() bool { return !flw.active.Load() }

func (flw *Follower) activate() { 
	if flw.active.CompareAndSwap(false, true) {
		flw.rf.log("Activated")
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
			term: flw.rf.term,
			isLeader: false,
		}

	case *StartCommandEvent:
		ev.ch <- &StartCommandReply { false, -1, -1 }

	case *AppendEntriesEvent:
		flw.appendEntries(ev)

	case *RequestVoteEvent:
		flw.requestVote(ev)

	case *HeartBeatTimeoutEvent:
		flw.rf.log("HeartBeat Timeout")
		flw.rf.transRole(candidateFromFollower)

	default:
		flw.rf.fatal("Unknown event type: %s", typeName(ev))
	}
}

// Possible entry status replied:
// 1. ENTRY_STALE, if the leader is stale.
// 2. ENTRY_FAILUEE, if the prev log info cannot match
// 3. ENTRY_SUCCESS, if the entry is well received.
func (flw *Follower) appendEntries(ev *AppendEntriesEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply

	// if args.Entry != nil {
	// 	flw.rf.log("AppendEntries RPC from [%d/%d], PrevLogInfo = [%d/%d], LeaderCommit = %d", args.Id, args.Term, args.PrevLogInfo.Index, args.PrevLogInfo.Term, args.LeaderCommit)
	// } else {
	// 	flw.rf.log("HeartBeat from [%d/%d], PrevLogInfo = [%d/%d], LeaderCommit = %d", args.Id, args.Term, args.PrevLogInfo.Index, args.PrevLogInfo.Term, args.LeaderCommit)
	// }
	
	if args.Term < flw.rf.term {
		reply.EntryStatus = ENTRY_STALE
		reply.Term = flw.rf.term
		return
	}

	if args.Term > flw.rf.term {
		flw.rf.setTerm(args.Term)
	}
	flw.tickHeartBeatTimer()

	if args.Snapshot != nil {
		flw.rf.logs.followerInstallSnapshot(args.Snapshot)
		reply.EntryStatus = ENTRY_MATCH
		return
	}

	reply.EntryStatus = flw.rf.logs.followerAppendEntries(args.SendEntries)
	
	if reply.EntryStatus == ENTRY_MATCH && args.EntryType == ENTRY_T_LOG {
		flw.rf.log("updateCommit to %d", args.LeaderCommit)
		flw.rf.logs.updateCommit(args.LeaderCommit)
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

	reply.VoterID = flw.rf.me
	switch {
	case args.Term < flw.rf.term:
		reply.Term = flw.rf.term
		reply.VoteStatus = VOTE_OTHER

	case args.Term == flw.rf.term:
		if args.CandidateID == flw.rf.voteFor {
			reply.VoteStatus = VOTE_GRANTED
			flw.rf.log("Receive a duplicate vote request, vote granted")
		} else {
			reply.VoteStatus = VOTE_OTHER
		}

	case args.Term > flw.rf.term:
		flw.rf.setTerm(args.Term)
		if flw.rf.logs.atLeastUpToDate(args.LastLogInfo) {
			flw.rf.log("Grant vote to %d", args.CandidateID)
			flw.rf.voteFor = args.CandidateID
			reply.VoteStatus = VOTE_GRANTED
			flw.tickHeartBeatTimer()
		} else {
			reply.VoteStatus = VOTE_OTHER
		}
	}
}

func (flw *Follower) tickHeartBeatTimer() {
	d := genRandomDuration(HEARTBEAT_TIMEOUT...)
	flw.rf.log("HeartBeat timeout after %s", d)
	if flw.hbTimer == nil || !flw.hbTimer.Reset(d) {
		flw.hbTimer = time.AfterFunc(d, func() {
			flw.rf.tryPutEv(&HeartBeatTimeoutEvent{}, flw)
		})
	}
}

func followerFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	return &Follower {
		rf: cd.rf,
	}
}

func followerFromLeader(r Role) Role {
	ld := r.(*Leader)
	return &Follower {
		rf: ld.rf,
	}
}
