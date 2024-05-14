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
<<<<<<< HEAD
	term int
	termLock sync.RWMutex
	receivedVotes atomic.Uint32

	status atomic.Value
	
	newLeaderInfo *LeaderInfo // set only when defeated in election .

	electionTimer *time.Timer

	voters []bool
	VOTES_TO_WIN uint32
	auditLock sync.Mutex

	reelectTimer *time.Timer

=======
>>>>>>> msg-queue
	rf *Raft
	votes int
	voters []bool

	active atomic.Bool

	elecTimer *time.Timer
}

func (*Candidate) role() RoleEnum { return CANDIDATE }

func (cd *Candidate) closed() bool { return !cd.active.Load() }

func (cd *Candidate) activate() { 
	if cd.active.CompareAndSwap(false, true) {
		cd.startElection()
		cd.rf.chLock.Unlock()
		cd.log("Activated")
	}
}

<<<<<<< HEAD
func (cd *Candidate) waitUpdate() {
	for cd.status.Load() == POLL_UPDATING {}
}

func (cd *Candidate) requestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf := cd.rf
	reply.VoterID = rf.me

	cd.termLock.RLock()
	term := cd.term
	cd.termLock.RUnlock()

	if args.Term > term {
		// cd.status.CompareAndSwap(POLLING, DEFEATED)
		cd.breakElection(DEFEATED)
		reply.VoteStatus = VOTE_GRANTED
=======
func (cd *Candidate) stop() bool {
	if cd.active.CompareAndSwap(true, false) {
		if cd.elecTimer != nil {
			cd.elecTimer.Stop()
		}
		cd.rf.chLock.Lock()
		cd.log("Stopped")
		return true
>>>>>>> msg-queue
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
		cd.log("Unknown event %s", typeName(ev))
	}

}

func (cd *Candidate) _log(f func(string, ...interface{}), format string, args ...interface{}) {
	f("[Candidate %d/%d/%d/%d] %s", cd.rf.me, cd.rf.term, cd.rf.logs.LLI(), cd.rf.logs.LCI(), fmt.Sprintf(format, args...))
}

func (cd *Candidate) log(format string, args ...interface{}) {
	cd._log(log.Printf, format, args...)
}
func (cd *Candidate) fatal(format string, args ...interface{}) {
	cd._log(log.Fatalf, format, args...)
}

func (cd *Candidate) setElecTimer() {
	d := genRandomDuration(ELECTION_TIMEOUT...)
	cd.log("Election timeout after %s", d)
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
<<<<<<< HEAD
	cd.log("Election info: last commit index = %d, last commit term = %d", args.LastLogIndex, args.LastLogTerm)
	
	election: for {

		if cd.status.CompareAndSwap(POLLING, POLL_UPDATING) {
			cd.termLock.Lock()
			cd.log("New round election")
			cd.term++
			cd.receivedVotes.Store(0)
			cd.voters = make([]bool, len(rf.peers))
			cd.setElectionTimer()
			cd.termLock.Unlock()

			// update RequestVoteArgs
			args.Term = cd.term
			
			assert(cd.status.Load() == POLL_UPDATING)
			cd.status.CompareAndSwap(POLL_UPDATING, POLLING)
		} else {
			cd.log("Break election as in status %s", cd.statusName())
			break election
		}

		cd.log("Election restart with term %d", cd.term)

		for i, peer := range rf.peers {
			if cd.status.Load() != POLLING { break }
			if i == rf.me { continue }

			go func(peer *labrpc.ClientEnd, args *RequestVoteArgs, peerId int, currTerm int) {
				reply := &RequestVoteReply {}				
				ok := peer.Call("Raft.RequestVote", args, reply)
				cd.log("Request vote from %d", peerId)
				
				for tries := RPC_CALL_TRY_TIMES;
					!ok && tries > 0 && cd.status.Load() == POLLING; 
					tries-- {

					time.Sleep(RPC_FAIL_WAITING)
					ok = peer.Call("Raft.RequestVote", args, reply)
					cd.log("Request vote from %d", peerId)
				}

				cd.termLock.RLock()
				if ok && currTerm == cd.term {
					cd.auditVote(reply)
				}
				cd.termLock.RUnlock()
			}(peer, args, i, cd.term)
		}

		for {
			switch cd.status.Load() {
			case POLLING:
				time.Sleep(CANDIDATE_CHECK_FREQ)
			case POLL_WAITING:
				time.Sleep(CANDIDATE_CHECK_FREQ)
			case POLL_NEW_ROUND:
				if cd.status.CompareAndSwap(POLL_NEW_ROUND, POLLING) {
					continue election
				}

			case POLL_TIMEOUT:
				cd.status.CompareAndSwap(POLL_TIMEOUT, POLL_WAITING)
				cd.setReelectTimer()

			case ELECTED:
				cd.log("Switch ELECTED")
				defer cd.winElection()
				break election

			case DEFEATED:
				cd.log("Switch DEFEATED")
				defer cd.electionFallback()
				break election

			case CANDIDATE_KILLED:
				break election

			default:
				cd.fatalf("Unknown status: %d", cd.status.Load().(CandidateStatus))
=======
	for i, peer := range rf.peers {
		if i == rf.me { continue }
		
		go func(peer *labrpc.ClientEnd, id int, term int, rf *Raft) {
			cd.log("Request vote from %d", id)
			reply := &RequestVoteReply {}

			ok := peer.Call("Raft.RequestVote", args, reply)

			for tries := RPC_CALL_TRY_TIMES;
				cd.active.Load() && (!ok || !reply.Responsed) && tries > 0;
				tries-- {
				time.Sleep(RPC_FAIL_WAITING)
				ok = peer.Call("Raft.RequestVote", args, reply)
>>>>>>> msg-queue
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
					cd.fatal("Unprocessed vote request from %d", reply.VoterID)
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
<<<<<<< HEAD
		voters: make([]bool, len(flw.rf.peers)),
		VOTES_TO_WIN: uint32(len(flw.rf.peers)/2),
		term: flw.term,
	}
	cd.status.Store(POLLING)
	return cd
}

func (cd *Candidate) setElectionTimer() {
	d := genRandomDuration(POLL_TIMEOUT_DURATION...)
	cd.log("Set electionTimer timeout duration = %s", d)
	if cd.electionTimer == nil || !cd.electionTimer.Reset(d) {
		cd.electionTimer = time.AfterFunc(d, func() {
			cd.status.CompareAndSwap(POLLING, POLL_TIMEOUT)
		})
=======
>>>>>>> msg-queue
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
		cd.log("Fallback to follower")
		cd.rf.setTerm(args.Term)
		cd.rf.transRole(followerFromCandidate)

		if cd.rf.logs.atLeastUpToDate(args.LastLogInfo) {
			reply.VoteStatus = VOTE_GRANTED
			cd.rf.voteFor = args.CandidateID
			cd.log("Grant Vote to %d", args.CandidateID)
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
			cd.log("Elected")
			cd.rf.transRole(leaderFromCandidate)
		}
	}
}
