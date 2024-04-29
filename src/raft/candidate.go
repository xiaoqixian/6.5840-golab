// Date:   Wed Apr 17 10:11:52 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
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
	term int
	rf *Raft
	votes int
	voters []bool

	active atomic.Bool

	elecTimer *time.Timer
}

func (*Candidate) role() RoleEnum { return CANDIDATE }

func (cd *Candidate) closed() bool { return !cd.active.Load() }

func (cd *Candidate) activate() { 
	cd.log("Activating")
	cd.active.Store(true)
	cd.rf.chLock.Unlock()
	cd.startElection()
	cd.log("Activated")
}

func (cd *Candidate) stop() {
	cd.active.Store(false)
	cd.rf.chLock.Lock()
}

func (cd *Candidate) process(ev Event) {
	switch ev := ev.(type) {
	case *GetStateEvent:
		ev.ch <- &NodeState {
			term: cd.term,
			isLeader: false,
		}

	case *StartCommandEvent:
		ev.ch <- nil

	case *AppendEntriesEvent:
		cd.appendEntries(ev)

	case *RequestVoteEvent:
		cd.requestVote(ev)

	case *VoteGrantEvent:
		cd.auditVote(ev)

	case *ElectionEvent:
		cd.startElection()

	default:
		cd.log("Unknown event %s", typeName(ev))
	}

}

func (cd *Candidate) log(format string, args ...interface{}) {
	log.Printf("[Candidate %d/%d] %s\n", cd.rf.me, cd.term, 
		fmt.Sprintf(format, args...))
}
func (cd *Candidate) fatal(format string, args ...interface{}) {
	log.Fatalf("[Candidate %d/%d] %s\n", cd.rf.me, cd.term, 
		fmt.Sprintf(format, args...))
}

func (cd *Candidate) setElecTimer() {
	d := genRandomDuration(ELECTION_TIMEOUT...)
	cd.log("Election timeout after %d", d)
	rf := cd.rf
	if cd.elecTimer == nil || cd.elecTimer.Reset(d) {
		cd.elecTimer = time.AfterFunc(d, func() {
			rf.tryPutEv(&ElectionEvent{}, cd)
		})
	}
}

func (cd *Candidate) startElection() {
	rf := cd.rf
	cd.term++
	cd.votes = 1
	cd.voters = make([]bool, len(rf.peers))

	args := &RequestVoteArgs {
		Term: cd.term,
		CandidateID: rf.me,
		LastCommitedIndex: rf.logs.LCI(),
	}
	for i, peer := range rf.peers {
		if i == rf.me { continue }
		
		go func(peer *labrpc.ClientEnd, id int, term int, rf *Raft) {
			cd.log("Request vote from %d", id)
			reply := &RequestVoteReply {}

			ok := peer.Call("Raft.RequestVote", args, reply)

			for tries := RPC_CALL_TRY_TIMES;
				!ok && tries > 0;
				tries-- {
				ok = peer.Call("Raft.RequestVote", args, reply)
			}
			
			if ok {
				switch reply.VoteStatus {
				case VOTE_GRANTED:
					rf.tryPutEv(&VoteGrantEvent {
						term: term,
						voter: reply.VoterID,
					}, cd)

				case VOTE_OTHER:
					if reply.Term > cd.term {
						rf.transRole(followerFromCandidate)
					}
					
				case VOTE_DENIAL:
					rf.transRole(followerFromCandidate)
					return

				case VOTE_DEFAULT:
					cd.fatal("Unprocessed vote request from %d", reply.VoterID)
				}

			}
		}(peer, i, cd.term, cd.rf)
	}

	cd.setElecTimer()
}

func candidateFromFollower(r Role) Role {
	flw := r.(*Follower)
	return &Candidate {
		term: flw.term,
		rf: flw.rf,
	}
}

func (cd *Candidate) appendEntries(ev *AppendEntriesEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	cd.log("AppendEntries RPC from [%d/%d]", args.Id, args.Term)
	
	switch {
	case args.Term >= cd.term:
		cd.term = args.Term
		cd.rf.transRole(followerFromLeader)
		reply.EntryStatus = ENTRY_HOLD

	case args.Term < cd.term:
		reply.EntryStatus = ENTRY_STALE
	}
}

func (cd *Candidate) requestVote(ev *RequestVoteEvent) {
	defer func() { ev.ch <- true }()
	args, reply := ev.args, ev.reply
	cd.log("RequestVote RPC from [%d/%d]", args.CandidateID, args.Term)

	switch {
	case args.Term <= cd.term:
		reply.VoteStatus = VOTE_OTHER // which is myself.
		reply.Term = cd.term

	case args.Term > cd.term:
		if args.LastCommitedIndex >= cd.rf.logs.LCI() {
			reply.VoteStatus = VOTE_GRANTED
		} else {
			reply.VoteStatus = VOTE_OTHER
		}

		cd.term = args.Term
		cd.rf.transRole(followerFromLeader)
	}
}

func (cd *Candidate) auditVote(ev *VoteGrantEvent) {
	if cd.term == ev.term && !cd.voters[ev.voter] {
		cd.voters[ev.voter] = true
		cd.votes++

		if cd.votes >= cd.rf.majorN {
			cd.rf.transRole(leaderFromCandidate)
		}
	}
}
