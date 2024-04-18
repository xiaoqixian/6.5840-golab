// Date:   Wed Apr 17 10:11:52 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"fmt"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"6.5840/labrpc"
)

type CandidateStatus uint8
const (
	POLLING CandidateStatus = iota
	ELECTED
	DEFEATED
	POLL_TIMEOUT
)

type VoteStatus uint8
const (
	VOTE_GRANTED VoteStatus = iota
	VOTE_OTHER
	VOTE_DENIAL // response VOTE_DENIAL cause follower is still following a leaer with a term >= candidate's temr.
)

type Candidate struct {
	term atomic.Int32
	receivedVotes atomic.Uint32

	status atomic.Value
	
	newLeaderInfo *LeaderInfo // set only when defeated in election .

	electionTimer *time.Timer

	voters []bool
	VOTES_TO_WIN uint32
	auditLock sync.Mutex

	rf *Raft
}

func (*Candidate) role() RoleEnum { return CANDIDATE }

func (cd *Candidate) init() {
	go cd.startElection()
}
func (*Candidate) finish() {}

func (*Candidate) name() string {
	return "Candidate"
}

func (cd *Candidate) requestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf := cd.rf
	reply.VoterID = rf.me
	term := int(cd.term.Load())

	if args.Term > term {
		cd.status.Store(DEFEATED)
		reply.VoteStatus = VOTE_GRANTED
	} else {
		reply.VoteStatus = VOTE_DENIAL
		reply.Term = term
	}
}

func (cd *Candidate) startElection() {
	cd.log("Start election")
	rf := cd.rf
	
	args := &RequestVoteArgs {
		CandidateID: rf.me,
		LastLogIndex: rf.logs.lastLogIndex(),
		LastLogTerm: rf.logs.lastLogTerm(),
	}
	
	election: for {
		cd.term.Add(1)
		cd.receivedVotes.Store(0)
		args.Term = int(cd.term.Load())

		for i, peer := range rf.peers {
			if i == rf.me { continue }

			go func(peer *labrpc.ClientEnd, args *RequestVoteArgs, peer_id int) {
				reply := &RequestVoteReply {}				
				ok := false
				tries := RPC_CALL_TRY_TIMES
				for !ok && tries > 0 && cd.status.Load() == POLLING {
					tries--
					ok = peer.Call("Raft.RequestVote", args, reply)
					cd.log("Request vote from %d", peer_id)
					if ok {
						cd.auditVote(reply)
					}
				}
			}(peer, args, i)
		}

		// waiting for election result.
		for {
			switch cd.status.Load() {
			case POLLING:
				time.Sleep(10 * time.Millisecond)

			case POLL_TIMEOUT:
				time.Sleep(genRandomDuration(ELECTION_WAITING))
				continue election

			case ELECTED:
				defer cd.winLeader()
				break election

			case DEFEATED:
				defer cd.electionFallback()
				break election
			}
		}
	}
}

func candidateFromFollower(r Role) Role {
	flw := r.(*Follower)
	cd := &Candidate {
		rf: flw.rf,
		voters: make([]bool, len(flw.rf.peers)),
		VOTES_TO_WIN: uint32(len(flw.rf.peers)/2),
	}
	cd.term.Store(int32(flw.leaderInfo.term))
	cd.status.Store(POLLING)
	return cd
}

func (cd *Candidate) setElectionTimer() {
	d := genRandomDuration(POLL_TIMEOUT_DURATION...)
	if cd.electionTimer == nil || !cd.electionTimer.Reset(d) {
		cd.electionTimer = time.AfterFunc(d, func() {
			cd.status.CompareAndSwap(POLLING, POLL_TIMEOUT)
		})
	}
}

func (cd *Candidate) winLeader() {
	cd.rf.transRole(leaderFromCandidate)
}

func (cd *Candidate) electionFallback() {
	cd.rf.transRole(followerFromCandidate)
}

// return true to abort vote requesting
func (cd *Candidate) auditVote(reply *RequestVoteReply) {
	switch reply.VoteStatus {
	case VOTE_GRANTED:
		cd.auditLock.Lock()
		defer cd.auditLock.Unlock()
		
		if cd.voters[reply.VoterID] { return }
		if cd.receivedVotes.Add(1) >= cd.VOTES_TO_WIN {
			cd.status.CompareAndSwap(POLLING, ELECTED)
		}
		cd.voters[reply.VoterID] = true

	case VOTE_OTHER:
		if reply.Term > int(cd.term.Load()) {
			cd.log("Defeated")
			cd.term.Store(int32(reply.Term))
			cd.status.CompareAndSwap(POLLING, DEFEATED)
		}

	case VOTE_DENIAL:
		if reply.Term >= int(cd.term.Load()) {
			cd.log("Defeated")
			cd.term.Store(int32(reply.Term))
			cd.status.CompareAndSwap(POLLING, DEFEATED)
		}
	}
}

// If the leader is valid, this RPC call will triger the abortion
// of the election, but the candidate will not handle the log entries 
// for the follower. 
// So the transformed follower may receive the same RPC call after 
// the leader has an AppendEntries timeout.
func (cd *Candidate) appendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if args.Term >= int(cd.term.Load()) {
		cd.status.CompareAndSwap(POLLING, DEFEATED)
	}
}

func (cd *Candidate) log(format string, args ...interface{}) {
	log.Printf("[Candidate %d/%d] %s\n", cd.rf.me, cd.term.Load(), fmt.Sprintf(format, args...))
}
