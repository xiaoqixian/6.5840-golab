// Date:   Wed Apr 17 10:11:52 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
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
	term int
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

func (cd *Candidate) startElection() {
	rf := cd.rf
	
	args := &RequestVoteArgs {
		CandidateID: rf.me,
		LastLogIndex: rf.logs.lastLogIndex(),
		LastLogTerm: rf.logs.lastLogTerm(),
	}
	
	election: for {
		cd.term++
		cd.receivedVotes.Store(0)
		args.Term = cd.term

		for i, peer := range rf.peers {
			if i == rf.me { continue }

			go func(peer *labrpc.ClientEnd, args *RequestVoteArgs) {
				reply := &RequestVoteReply {}				
				ok := false
				tries := RPC_CALL_TRY_TIMES
				for !ok && tries > 0 && cd.status.Load() == POLLING {
					tries--
					ok = peer.Call("Raft.RequestVote", args, reply)
					log.Printf("[Candidate %d] Request vote from %d\n", rf.me, i)
					if ok {
						cd.auditVote(reply)
					}
				}
			}(peer, args)
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

func candidateFromFollower(flw *Follower) *Candidate {
	cd := &Candidate {
		term: flw.leaderInfo.term,
		rf: flw.rf,
		voters: make([]bool, len(flw.rf.peers)),
		VOTES_TO_WIN: uint32(len(flw.rf.peers)/2),
	}
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
		if reply.Term > cd.term {
			log.Printf("[Candidate] Defeated\n")
			cd.term = reply.Term
			cd.status.CompareAndSwap(POLLING, DEFEATED)
		}

	case VOTE_DENIAL:
		if reply.Term >= cd.term {
			log.Printf("[Candidate] Defeated\n")
			cd.term = reply.Term
			cd.status.CompareAndSwap(POLLING, DEFEATED)
		}
	}
}

