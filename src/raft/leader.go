// Date:   Wed Apr 17 09:40:48 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
	"sync/atomic"

	"6.5840/labrpc"
)

type Leader struct {
	term int
	rf *Raft

	active atomic.Bool

	nextIndex []int
	
	hbTimer *RepeatTimer

	rc *ReplCounter
}

func (*Leader) role() RoleEnum { return LEADER }

func (ld *Leader) closed() bool { return !ld.active.Load() }

func (ld *Leader) activate() {
	rf := ld.rf
	ld.active.Store(true)
	rf.chLock.Unlock()

	// remove uncommited logs
	rf.logs.removeUncommittedTail()
	// add a noop log entry.
	logIndex, _ := rf.logs.leaderAppendEntry(&LogEntry {
		Type: ENTRY_NOOP,
		Term: ld.term,
	})
	ld.rc.watchIndex(logIndex)

	ld.hbTimer = newRepeatTimer(HEARTBEAT_SEND, func() {
		ld.rf.tryPutEv(&SendHeartBeatEvent{}, ld)
	})

	ld.startReplication()
}

func (ld *Leader) stop() {
	ld.rf.chLock.Lock()
	ld.active.Store(false)
	if ld.hbTimer != nil {
		ld.hbTimer.kill()
	}
}

func (ld *Leader) process(ev Event) {
	switch ev := ev.(type) {
	case *GetStateEvent:
		ev.ch <- &NodeState {
			term: ld.term,
			isLeader: true,
		}

	case *StartCommandEvent:
		logIndex, commandIndex := ld.rf.logs.leaderAppendEntry(&LogEntry {
			Type: ENTRY_NORMAL,
			Term: ld.term,
			Content: ev.command,
		})
		ev.ch <- &StartCommandReply {
			ok: true,
			term: ld.term,
			index: commandIndex,
		}
		ld.rc.watchIndex(logIndex)

	case *AppendEntriesEvent:
		ld.appendEntries(ev)

	case *RequestVoteEvent:
		ld.requestVote(ev)

	case *SendHeartBeatEvent:
		ld.sendHeartBeat()

	case *ReplConfirmEvent:
		ld.rc.confirm(ev.index, ev.id)

	default:
		ld.fatal("Unknown event type: %s", typeName(ev))
	}
}

func (ld *Leader) log(format string, args ...interface{}) {
	log.Printf("[Follower %d/%d] %s", ld.rf.me, ld.term, fmt.Sprintf(format, args...))
}
func (ld *Leader) fatal(format string, args ...interface{}) {
	log.Fatalf("[Follower %d/%d] %s", ld.rf.me, ld.term, fmt.Sprintf(format, args...))
}

func leaderFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	nextIndex := make([]int, len(cd.rf.peers))
	lci := cd.rf.logs.LCI()
	for i, _ := range cd.rf.peers {
		nextIndex[i] = lci
	}

	return &Leader {
		term: cd.term,
		rf: cd.rf,
		nextIndex: nextIndex,
		rc: newReplCounter(cd.rf),
	}
}

func (ld *Leader) sendHeartBeat() {
	rf := ld.rf
	args := &AppendEntriesArgs {
		Id: rf.me,
		Term: ld.term,
		LeaderCommit: rf.logs.LCI(),
	}

	for i, peer := range rf.peers {
		if i == rf.me { continue }
		
		go func(peer *labrpc.ClientEnd, id int) {
			reply := &AppendEntriesReply {}
			ok := peer.Call("Raft.AppendEntries", args, reply)
			for tries := RPC_CALL_TRY_TIMES;
				!ok && tries > 0;
				tries-- {
				ok = peer.Call("Raft.AppendEntries", args, reply)
			}

			if ok && reply.EntryStatus == ENTRY_STALE {
				ld.retire()
			}
		}(peer, i)
	}
}

func (ld *Leader) retire() {
	if ld.active.CompareAndSwap(true, false) {
		ld.log("Retire")
		ld.rf.transRole(followerFromLeader)
	}
}

func (ld *Leader) appendEntries(ev *AppendEntriesEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	ld.log("AppendEntries RPC from [%d/%d]", args.Id, args.Term)
	reply.Term = ld.term

	switch {
	case args.Term < ld.term:
		reply.EntryStatus = ENTRY_STALE

	case args.Term == ld.term:
		ld.fatal("Two leaders with a same term")

	case args.Term > ld.term:
		reply.EntryStatus = ENTRY_HOLD
		ld.term = args.Term
		ld.retire()
	}
}

func (ld *Leader) requestVote(ev *RequestVoteEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	ld.log("RequestVote RPC from [%d/%d]", args.CandidateID, args.Term)
	reply.Term = ld.term

	switch {
	case args.Term <= ld.term:
		reply.VoteStatus = VOTE_DENIAL
		ld.log("Vote denialed for %d", args.CandidateID)
		
	case args.Term > ld.term:
		if args.LastCommitedIndex >= ld.rf.logs.LCI() {
			reply.VoteStatus = VOTE_GRANTED
			ld.log("Vote granted to %d", args.CandidateID)
		} else {
			reply.VoteStatus = VOTE_OTHER
			ld.log("I'd like to vote to others")
		}
	}
}

func (ld *Leader) startReplication() {
	for i, peer := range ld.rf.peers {
		if i == ld.rf.me { continue }
		
		go newReplicator(peer, i, ld).start()
	}
}
