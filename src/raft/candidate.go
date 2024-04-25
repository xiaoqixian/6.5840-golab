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
	CANDIDATE_KILLED

	POLL_UPDATING
	POLL_WAITING
	POLL_NEW_ROUND
)

func candidateStatusToString(s CandidateStatus) string {
	switch s {
	case POLLING:
		return "POLLING"
	case ELECTED:
		return "ELECTED"
	case DEFEATED:
		return "DEFEATED"
	case POLL_TIMEOUT:
		return "POLL_TIMEOUT"
	case CANDIDATE_KILLED:
		return "CANDIDATE_KILLED"
	case POLL_UPDATING:
		return "POLL_UPDATING"
	case POLL_WAITING:
		return "POLL_WAITING"
	case POLL_NEW_ROUND:
		return "POLL_NEW_ROUND"
	default:
		return fmt.Sprintf("Unknown %d", s)
	}
}

type VoteStatus uint8
const (
	VOTE_DEFAULT VoteStatus = iota
	// VOTE_DENIAL is given by leader with at least as large term,
	// the candidate should quit election on receiving this vote.
	VOTE_DENIAL
	VOTE_GRANTED
	//  VOTE_OTHER is given by follower who has given its vote to a 
	// candidate with at least as large term.
	VOTE_OTHER
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

	reelectTimer *time.Timer

	rf *Raft
}

func (*Candidate) role() RoleEnum { return CANDIDATE }

func (cd *Candidate) init() {
	go cd.startElection()
}
func (*Candidate) finish() {}

func (cd *Candidate) kill() {
	cd.status.Store(CANDIDATE_KILLED)
	cd.log("Killed")
}

func (*Candidate) name() string {
	return "Candidate"
}

func (cd *Candidate) statusName() string {
	return candidateStatusToString(cd.status.Load().(CandidateStatus))
}

func (cd *Candidate) setReelectTimer() {
	d := genRandomDuration(ELECTION_TIMEOUT_WAITING_DURATION...)
	cd.log("Election timeout, reelect after %s", d)
	if cd.reelectTimer == nil || !cd.reelectTimer.Reset(d) {
		cd.reelectTimer = time.AfterFunc(d, func() {
			cd.status.CompareAndSwap(POLL_WAITING, POLL_NEW_ROUND)
		})
	}
} 

func (cd *Candidate) breakElection(s CandidateStatus) bool {
	return cd.status.CompareAndSwap(POLLING, s) ||
		cd.status.CompareAndSwap(POLL_WAITING, s) ||
		cd.status.CompareAndSwap(POLL_NEW_ROUND, s)
}

func (cd *Candidate) waitUpdate() {
	for cd.status.Load() == POLL_UPDATING {}
}

func (cd *Candidate) requestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	rf := cd.rf
	reply.VoterID = rf.me

	cd.waitUpdate()
	term := int(cd.term.Load())

	if args.Term > term {
		// cd.status.CompareAndSwap(POLLING, DEFEATED)
		cd.breakElection(DEFEATED)
		reply.VoteStatus = VOTE_GRANTED
	} else {
		reply.VoteStatus = VOTE_OTHER
		reply.Term = term
	}
}

func (cd *Candidate) startElection() {
	cd.log("Start election")
	rf := cd.rf
	
	args := &RequestVoteArgs {
		CandidateID: rf.me,
		LastLogIndex: rf.logs.lastCommitedLogIndex(),
		LastLogTerm: rf.logs.lastCommitedLogTerm(),
	}
	cd.log("Election info: last commit index = %d, last commit term = %d", args.LastLogIndex, args.LastLogTerm)
	
	election: for {
		if cd.status.CompareAndSwap(POLLING, POLL_UPDATING) {
			cd.log("New round election")
			cd.term.Add(1)
			cd.receivedVotes.Store(0)
			cd.voters = make([]bool, len(rf.peers))
			cd.setElectionTimer()

			// update RequestVoteArgs
			args.Term = int(cd.term.Load())
			
			assert(cd.status.Load() == POLL_UPDATING)
			cd.status.CompareAndSwap(POLL_UPDATING, POLLING)
		} else {
			cd.log("Break election as in status %s", cd.statusName())
			break election
		}

		cd.log("Election restart with term %d", cd.term.Load())

		for i, peer := range rf.peers {
			if cd.status.Load() != POLLING { break }
			if i == rf.me { continue }

			go func(peer *labrpc.ClientEnd, args *RequestVoteArgs, peerId int, currTerm int32) {
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
				if ok && currTerm == cd.term.Load() {
					cd.auditVote(reply)
				}
			}(peer, args, i, cd.term.Load())
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
	cd.log("Set electionTimer timeout duration = %s", d)
	if cd.electionTimer == nil || !cd.electionTimer.Reset(d) {
		cd.electionTimer = time.AfterFunc(d, func() {
			cd.status.CompareAndSwap(POLLING, POLL_TIMEOUT)
		})
	}
}

func (cd *Candidate) winElection() {
	cd.log("Wins election")
	cd.rf.transRole(leaderFromCandidate)
}

func (cd *Candidate) electionFallback() {
	cd.log("Lost election")
	cd.rf.transRole(followerFromCandidate)
}

// return true to abort vote requesting
func (cd *Candidate) auditVote(reply *RequestVoteReply) {
	switch reply.VoteStatus {
	case VOTE_GRANTED:
		cd.log("Vote granted by %d", reply.VoterID)

		cd.auditLock.Lock()
		defer cd.auditLock.Unlock()
		
		if cd.voters[reply.VoterID] { return }
		if cd.receivedVotes.Add(1) >= cd.VOTES_TO_WIN {
			// cd.status.CompareAndSwap(POLLING, ELECTED)
			cd.breakElection(ELECTED)
		}
		cd.voters[reply.VoterID] = true

	case VOTE_OTHER:
		if reply.Term > int(cd.term.Load()) {
			cd.log("Defeated by other node with term %d", reply.Term)
			cd.term.Store(int32(reply.Term))
			// cd.status.CompareAndSwap(POLLING, DEFEATED)
			cd.breakElection(DEFEATED)
		}

	case VOTE_DENIAL:
		if reply.Term >= int(cd.term.Load()) {
			cd.log("Vote denied by %d with term %d", reply.VoterID, reply.Term)
			cd.term.Store(int32(reply.Term))
			// cd.status.CompareAndSwap(POLLING, DEFEATED)
			cd.breakElection(DEFEATED)
		}

	default:
		log.Fatalf("Unprocessed VoteStatus = %d", reply.VoteStatus)
	}
}

// If the leader is valid, this RPC call will triger the abortion
// of the election, but the candidate will not handle the log entries 
// for the follower. 
// So the transformed follower may receive the same RPC call after 
// the leader has an AppendEntries timeout.
func (cd *Candidate) appendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if args.Entries == nil {
		cd.log("Receive HeartBeat from [%d/%d], LeaderCommit = %d", args.Id, args.Term, args.LeaderCommit)
	} else {
		cd.log("Receive AppendEntries request from [%d/%d], prev log info [%d/%d], LeaderCommit = %d.", args.Id, args.Term, args.PrevLogIndex, args.PrevLogTerm, args.LeaderCommit)
	}

	if args.Term >= int(cd.term.Load()) {
		cd.log("Defeated by leader [%d/%d]", args.Id, args.Term)
		// cd.status.CompareAndSwap(POLLING, DEFEATED)
		cd.breakElection(DEFEATED)
		cd.log("Status: %s", candidateStatusToString(cd.status.Load().(CandidateStatus)))
	} else {
		cd.log("Inform [%d/%d] is a stale leader", args.Id, args.Term)
		reply.Term = int(cd.term.Load())
	}
}

func (cd *Candidate) log(format string, args ...interface{}) {
	log.Printf("[Candidate %d/%d/%d] %s\n", cd.rf.me, cd.term.Load(), cd.rf.logs.lastCommitedIndex.Load(), fmt.Sprintf(format, args...))
	// log.Printf("[Candidate %d/%d] " + format + "\n", cd.rf.me, cd.term.Load())
}

func (cd *Candidate) fatalf(format string, args ...interface{}) {
	log.Fatalf("[Candidate %d/%d/%d] %s\n", cd.rf.me, cd.term.Load(), cd.rf.logs.lastCommitedIndex.Load(), fmt.Sprintf(format, args...))
}
