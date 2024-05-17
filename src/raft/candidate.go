// Date:   Wed Apr 17 10:11:52 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"sync/atomic"
	"time"

	"6.5840/labrpc"
)

type VoteStatus uint8
const (
	VOTE_DEFAULT VoteStatus = iota
	VOTE_OTHER
	VOTE_GRANTED
	VOTE_DENIAL
)

type Candidate struct {
	rf *Raft
	votes int
	voters []bool

	active atomic.Bool

	elecTimer *time.Timer
}

func (*Candidate) role() RoleEnum { return CANDIDATE }
func (*Candidate) name() string { return "Candidate" }

func (cd *Candidate) closed() bool { return !cd.active.Load() }

func (cd *Candidate) activate() { 
	if cd.active.CompareAndSwap(false, true) {
		cd.startElection()
		cd.rf.chLock.Unlock()
		cd.rf.log("Activated")
	}
}

func (cd *Candidate) stop() bool {
	if cd.active.CompareAndSwap(true, false) {
		if cd.elecTimer != nil {
			cd.elecTimer.Stop()
		}
		cd.rf.chLock.Lock()
		cd.rf.log("Stopped")
		return true
	} else {
		return false
	}
}

func (cd *Candidate) process(ev Event) {
	switch ev := ev.(type) {
	case *GetStateEvent:
		ev.ch <- &NodeState {
			term: cd.rf.Term(),
			isLeader: false,
		}

	case *StartCommandEvent:
		ev.ch <- &StartCommandReply { false, -1, -1 }

	case *AppendEntriesEvent:
		cd.appendEntries(ev)

	case *RequestVoteEvent:
		cd.requestVote(ev)

	case *VoteGrantEvent:
		cd.auditVote(ev)

	case *ElectionTimeoutEvent:
		cd.rf.transRole(followerFromCandidate)

	case *StaleCandidateEvent:
		if ev.newTerm > cd.rf.term {
			cd.rf.setTerm(ev.newTerm)
		}
		cd.rf.transRole(followerFromCandidate)

	default:
		cd.rf.log("Unknown event %s", typeName(ev))
	}

}

func (cd *Candidate) setElecTimer() {
	d := genRandomDuration(ELECTION_TIMEOUT...)
	cd.rf.log("Election timeout after %s", d)
	rf := cd.rf
	if cd.elecTimer == nil || cd.elecTimer.Reset(d) {
		cd.elecTimer = time.AfterFunc(d, func() {
			rf.tryPutEv(&ElectionTimeoutEvent{}, cd)
		})
	}
}

func (cd *Candidate) startElection() {
	rf := cd.rf
	cd.rf.setTerm(cd.rf.term+1)
	cd.votes = 1
	cd.voters = make([]bool, len(rf.peers))

	args := &RequestVoteArgs {
		Term: cd.rf.term,
		CandidateID: rf.me,
		LastLogInfo: rf.logs.lastLogInfo(),
	}
	for i, peer := range rf.peers {
		if i == rf.me { continue }
		
		go func(peer *labrpc.ClientEnd, id int, term int, rf *Raft) {
			cd.rf.log("Request vote from %d", id)
			reply := &RequestVoteReply {}

			ok := peer.Call("Raft.RequestVote", args, reply)

			for tries := RPC_CALL_TRY_TIMES;
				cd.active.Load() && (!ok || !reply.Responsed) && tries > 0;
				tries-- {
				time.Sleep(RPC_FAIL_WAITING)
				ok = peer.Call("Raft.RequestVote", args, reply)
			}

			if !cd.active.Load() { return }
			
			if ok && reply.Responsed {
				switch reply.VoteStatus {
				case VOTE_GRANTED:
					rf.tryPutEv(&VoteGrantEvent {
						term: term,
						voter: reply.VoterID,
					}, cd)

				case VOTE_OTHER:
					if reply.Term > cd.rf.term {
						rf.tryPutEv(&StaleCandidateEvent{reply.Term}, cd)
					}
					
				case VOTE_DENIAL:
					rf.tryPutEv(&StaleCandidateEvent{cd.rf.term}, cd)

				case VOTE_DEFAULT:
					cd.rf.fatal("Unprocessed vote request from %d", reply.VoterID)
				}

			}
		}(peer, i, cd.rf.term, cd.rf)
	}

	cd.setElecTimer()
}

func candidateFromFollower(r Role) Role {
	flw := r.(*Follower)
	return &Candidate {
		rf: flw.rf,
	}
}

func (cd *Candidate) appendEntries(ev *AppendEntriesEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	
	switch {
	case args.Term >= cd.rf.term:
		reply.EntryStatus = ENTRY_HOLD
		if args.Term > cd.rf.term {
			cd.rf.setTerm(args.Term)
		}
		cd.rf.transRole(followerFromCandidate)

	case args.Term < cd.rf.term:
		reply.EntryStatus = ENTRY_STALE
		reply.Term = cd.rf.term
	}
}

func (cd *Candidate) requestVote(ev *RequestVoteEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply

	switch {
	case args.Term < cd.rf.term:
		reply.VoteStatus = VOTE_OTHER // which is myself.
		reply.Term = cd.rf.term

	case args.Term == cd.rf.term:
		assert(cd.rf.voteFor == -1)
		reply.Term = args.Term
		if args.CandidateID == cd.rf.voteFor {
			reply.VoteStatus = VOTE_GRANTED
		} else {
			reply.VoteStatus = VOTE_OTHER
		}

	case args.Term > cd.rf.term:
		cd.rf.log("Fallback to follower")
		cd.rf.setTerm(args.Term)
		cd.rf.transRole(followerFromCandidate)

		if cd.rf.logs.atLeastUpToDate(args.LastLogInfo) {
			reply.VoteStatus = VOTE_GRANTED
			cd.rf.voteFor = args.CandidateID
			cd.rf.log("Grant Vote to %d", args.CandidateID)
		} else {
			reply.VoteStatus = VOTE_OTHER
		}
	}
}

func (cd *Candidate) auditVote(ev *VoteGrantEvent) {
	if cd.rf.term == ev.term && !cd.voters[ev.voter] {
		cd.voters[ev.voter] = true
		cd.votes++

		if cd.votes >= cd.rf.majorN {
			cd.rf.log("Elected")
			cd.rf.transRole(leaderFromCandidate)
		}
	}
}
