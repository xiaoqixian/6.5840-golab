// Date:   Wed Apr 17 09:40:48 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
	"sync/atomic"
)

type Leader struct {
	rf *Raft

	active atomic.Bool

	rc *ReplCounter
}
func (*Leader) role() RoleEnum { return LEADER }

func (ld *Leader) closed() bool { return !ld.active.Load() }

func (ld *Leader) activate() {
	rf := ld.rf

	// watch uncommited entry indcies
	// lci, lli := rf.logs.LCI(), rf.logs.LLI()
	// for idx := lci+1; idx < lli; idx++ {
	// 	ld.rc.watchIndex(idx)
	// }

	// remove uncommited logs
	// rf.logs.removeUncommittedTail()
	// add a noop log entry.
	logIndex, _ := rf.logs.leaderAppendEntry(LogEntry {
		CommandIndex: NOOP_INDEX,
		Term: ld.rf.term,
	})
	ld.log("Add NoopEntry with index = %d", logIndex)
	ld.rc.watchIndex(logIndex)

	// ld.hbTimer = newRepeatTimer(HEARTBEAT_SEND, func() {
	// 	ld.rf.tryPutEv(&SendHeartBeatEvent{}, ld)
	// })

	ld.startReplication()

	ld.active.Store(true)
	rf.chLock.Unlock()
}

func (ld *Leader) stop() bool {
	if ld.active.CompareAndSwap(true, false) {
		ld.rf.chLock.Lock()
		return true
	} else {
		return false
	}
}

func (ld *Leader) process(ev Event) {
	switch ev := ev.(type) {
	case *GetStateEvent:
		ev.ch <- &NodeState {
			term: ld.rf.term,
			isLeader: true,
		}

	case *StartCommandEvent:
		logIndex, commandIndex := ld.rf.logs.leaderAppendEntry(LogEntry {
			Term: ld.rf.term,
			Content: ev.command,
		})
		ev.ch <- &StartCommandReply {
			ok: true,
			term: ld.rf.term,
			index: commandIndex,
		}
		ld.log("Start command with index = %d, commandIndex = %d", logIndex, commandIndex)
		ld.rc.watchIndex(logIndex)

	case *AppendEntriesEvent:
		ld.appendEntries(ev)

	case *RequestVoteEvent:
		ld.requestVote(ev)

	case *ReplConfirmEvent:
		ld.rc.confirm(ev.id, ev.startIndex, ev.endIndex)

	case *StaleLeaderEvent:
		ld.rf.setTerm(ev.newTerm)
		ld.rf.transRole(followerFromLeader)

	default:
		ld.fatal("Unknown event type: %s", typeName(ev))
	}
}

func (ld *Leader) _log(f func(string, ...interface{}), format string, args ...interface{}) {
	f("[Leader %d/%d/%d/%d/%d] %s", 
	ld.rf.me, ld.rf.term, ld.rf.logs.LLI(), 
	ld.rf.logs.LCI(), len(ld.rf.logs.entries), 
	fmt.Sprintf(format, args...))
}

func (ld *Leader) log(format string, args ...interface{}) {
	ld._log(log.Printf, format, args...)
}
func (ld *Leader) fatal(format string, args ...interface{}) {
	ld._log(log.Fatalf, format, args...)
}

func leaderFromCandidate(r Role) Role {
	cd := r.(*Candidate)

	return &Leader {
		rf: cd.rf,
		rc: newReplCounter(cd.rf),
	}
}


func (ld *Leader) appendEntries(ev *AppendEntriesEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	reply.Term = ld.rf.term

	switch {
	case args.Term < ld.rf.term:
		reply.EntryStatus = ENTRY_STALE

	case args.Term == ld.rf.term:
		ld.fatal("Two leaders with a same term")

	case args.Term > ld.rf.term:
		reply.EntryStatus = ENTRY_HOLD
		ld.rf.setTerm(args.Term)
		ld.rf.transRole(followerFromLeader)
	}
}

func (ld *Leader) requestVote(ev *RequestVoteEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	ld.log("RequestVote RPC from [%d/%d], lli = [%d/%d]", args.CandidateID, args.Term, args.LastLogInfo.Index, args.LastLogInfo.Term)
	reply.Term = ld.rf.term

	switch {
	case args.Term <= ld.rf.term:
		assert(ld.rf.voteFor == -1)
		reply.VoteStatus = VOTE_DENIAL
		ld.log("Vote denialed for %d", args.CandidateID)
		
	case args.Term > ld.rf.term:
		ld.rf.setTerm(args.Term)
		if ld.rf.logs.atLeastUpToDate(args.LastLogInfo) {
			reply.VoteStatus = VOTE_GRANTED
			ld.rf.voteFor = args.CandidateID
			ld.log("Vote granted to %d", args.CandidateID)
		} else {
			reply.VoteStatus = VOTE_OTHER
			ld.log("I'd like to vote to others")
		}
		ld.rf.transRole(followerFromLeader)
	}
}

func (ld *Leader) startReplication() {
	for i, peer := range ld.rf.peers {
		if i == ld.rf.me { continue }
		
		go newReplicator(peer, i, ld).start()
	}
}
