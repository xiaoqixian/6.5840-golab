// Date:   Wed Apr 17 09:40:48 2024
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

// information for a leader transform to a follower.
type RetireInfo struct {
	term int
}

type ReplEntry struct {
	bitVec *BitVec
	index int
}

type ReplConfirm struct {
	index int
	peerId int
}

type ReplCounter struct {
	replEntries []ReplEntry
	majorN int
	peerNum int
	sync.RWMutex
}

type Leader struct {
	term int

	rf *Raft

	heartBeatTimer *RepeatTimer

	retireInfo *RetireInfo

	// each goroutine only access a specific element, 
	// so it is still thread-safe without the protection of lock.
	nextIndex []int
	replCounter *ReplCounter

	retired atomic.Bool

	confirmCh chan ReplConfirm
}

func leaderFromCandidate(r Role) Role {
	cd := r.(*Candidate)
	rf := cd.rf
	nextIndex := make([]int, len(rf.peers))
	lastCommitedLogIndex := rf.logs.lastCommitedLogIndex()
	for i := range rf.peers {
		nextIndex[i] = lastCommitedLogIndex + 1
	}
	return &Leader {
		term: int(cd.term.Load()),
		rf: rf,
		nextIndex: nextIndex,
		replCounter: newReplCounter(len(rf.peers)/2, len(rf.peers)),
	}
}

func (*Leader) role() RoleEnum { return LEADER }

func (ld *Leader) init() {
	ld.log("Come to power, last log index = %d, last log term = %d", ld.rf.logs.lastCommitedLogIndex(), ld.rf.logs.lastCommitedLogTerm())

	ld.setHeartbeatTimer()
	ld.startReplication()

	ld.rf.logs.removeUncommitedTail()

	ld.confirmCh = make(chan ReplConfirm)
	go ld.confirmTicker()
	go ld.addNoopEntry()
}
func (ld *Leader) finish() {
	ld.retired.Store(true)
	if ld.heartBeatTimer != nil {
		ld.heartBeatTimer.kill()
	}
}

func (ld *Leader) kill() {
	ld.finish()
	ld.log("Killed")
}

func (*Leader) name() string {
	return "Leader"
}

// TODO: should a stale leader vote
func (ld *Leader) requestVote(args *RequestVoteArgs, reply *RequestVoteReply) {
	ld.log("RequestVote from %d with term %d, lastCommitedIndex = %d", args.CandidateID, args.Term, args.LastLogIndex)
	rf := ld.rf
	reply.VoterID = rf.me

	if args.Term > ld.term {
		// leader expired
		if args.LastLogIndex >= int(ld.rf.logs.lastCommitedIndex.Load()) {
			ld.log("Grant vote to %d with last commit index = %d", args.CandidateID, args.LastLogIndex)
			reply.VoteStatus = VOTE_GRANTED
		} else {
			reply.VoteStatus = VOTE_OTHER
			reply.Term = args.Term
		}
		ld.log("Retire cause of stale term")
		ld.term = args.Term
		ld.retire()
	} else {
		reply.VoteStatus = VOTE_DENIAL
		reply.Term = ld.term
	}
}

// An AppendEntries RPC call with a greater term will triger the 
// transformation from Leader to Follwer.
func (ld *Leader) appendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	ld.log("AppendEntries request from [%d, %d]", args.Id, args.Term)
	if args.Term > ld.term {
		ld.retire()
	} else {
		ld.log("%d is a stale leader", args.Id)
	}
}

func (ld *Leader) retire() {
	if ld.retired.CompareAndSwap(false, true) {
		ld.log("Retire")
		ld.retireInfo = &RetireInfo {
			term: ld.term,
		}
		ld.rf.transRole(followerFromLeader)
	}
}

func (ld *Leader) sendHeartBeats() {
	rf := ld.rf
	args := &AppendEntriesArgs {
		Id: rf.me,
		Term: ld.term,
		LeaderCommit: int(ld.rf.logs.lastCommitedIndex.Load()),
	}
	reply := &AppendEntriesReply {}

	for i, peer := range rf.peers {
		if i == rf.me { continue }
		go func(peer *labrpc.ClientEnd, id int, flag *atomic.Bool) {
			ld.log("Send heartbeat to %d", id)
			ok := peer.Call("Raft.AppendEntries", args, reply)
			tries := RPC_CALL_TRY_TIMES
			for !ok && tries > 0 && !flag.Load() {
				time.Sleep(RPC_FAIL_WAITING)
				tries--
				ok = peer.Call("Raft.AppendEntries", args, reply)
			}
			// TODO: process AppendEntriesReply
			if !reply.Success && reply.Term > ld.term {
				ld.log("Retire cause of stale term %d < %d", ld.term, reply.Term)
				ld.term = reply.Term
				ld.retire()
				return
			}
		}(peer, i, &ld.retired)
	}
}

func (ld *Leader) setHeartbeatTimer() {
	if ld.heartBeatTimer == nil {
		ld.heartBeatTimer = newRepeatTimer(HEARTBEAT_SEND, ld.sendHeartBeats)
	} else {
		ld.heartBeatTimer.reset(HEARTBEAT_SEND)
	}
}

func (ld *Leader) addEntry(command interface{}) (int, int) {
	entry := &LogEntry {
		Term: ld.term,
		Content: command,
	}
	commandIndex, logIndex := ld.rf.logs.appendEntry(entry)

	ld.log("Add normal entry with index = %d, command index = %d", logIndex, commandIndex)
	ld.watchEntries(logIndex)
	return commandIndex, ld.term
}

func (ld *Leader) addNoopEntry() {
	entry := &LogEntry {
		Term: ld.term,
		Content: NoopEntry {},
	}
	_, logIndex := ld.rf.logs.appendEntry(entry)
	ld.log("Add NoopEntry with index = %d", logIndex)
	ld.watchEntries(logIndex)
}

func (ld *Leader) startReplication() {
	for i, peer := range ld.rf.peers {
		if i == ld.rf.me { continue }
		go ld.replicateTo(peer, i)
	}
}

func (ld *Leader) replicateTo(peer *labrpc.ClientEnd, id int) {
	ld.log("Start replication to %d", id)
	rf := ld.rf
	replication: for !ld.retired.Load() {
		nextIndex := ld.nextIndex[id]
		ld.log("nextIndex[%d] = %d, lastLogIndex = %d", id, nextIndex, rf.logs.lastLogIndex())
		assert(nextIndex >= 0)
		if rf.logs.lastLogIndex() < nextIndex {
			time.Sleep(NEW_LOG_CHECK_FREQ)
			continue replication
		}

		success := false
		args := &AppendEntriesArgs {
			Id: rf.me,
			Term: ld.term,
			PrevLogIndex: nextIndex-1,
			PrevLogTerm: rf.logs.indexLogTerm(nextIndex-1),
			LeaderCommit: rf.logs.lastCommitedLogIndex(),
			Entries: rf.logs.indexLogEntry(nextIndex),
		}
		reply := &AppendEntriesReply {}

		for !success && !ld.retired.Load() {
			ld.log("Issue log entry %d to peer %d", nextIndex, id)
			ld.log("PrevLogIndex = %d, PrevLogTerm = %d", args.PrevLogIndex, args.PrevLogTerm)

			ok := peer.Call("Raft.AppendEntries", args, reply)
			for !ok {
				time.Sleep(RPC_FAIL_WAITING)
				ok = peer.Call("Raft.AppendEntries", args, reply)
			}

			// after a long time waiting, we should check if the leader 
			// is retired yet.
			if ld.retired.Load() { return }

			success = reply.Success
			ld.log("Peer = %d, nextIndex = %d, success = %t", id, nextIndex, success)

			if !success && reply.Term > ld.term {
				ld.log("Retire cause of stale term %d < %d", reply.Term, ld.term)
				ld.term = reply.Term
				defer ld.retire()
				return
			}

			if !success {
				nextIndex = maxInt(0, nextIndex-1)
				args.PrevLogIndex = nextIndex-1
				args.PrevLogTerm = rf.logs.indexLogTerm(nextIndex-1)
				args.Entries = rf.logs.indexLogEntry(nextIndex)

				ld.log("Next index for %d fallback to %d", id, nextIndex)
			}
		}

		ld.confirmCh <- ReplConfirm {
			index: nextIndex,
			peerId: id,
		}
		ld.nextIndex[id] = nextIndex+1
	}
}

func (ld *Leader) confirmTicker() {
	replCounter := ld.replCounter

	for !ld.retired.Load() {
		confirm := <- ld.confirmCh
		ld.log("%d confirmed log %d", confirm.peerId, confirm.index)

		replCounter.RLock()
		baseIndex := replCounter.replEntries[0].index
		offset := confirm.index - baseIndex
		ld.log("baseIndex = %d, offset = %d", baseIndex, offset)
		// this log is cleared because it's replicated to 
		// the majority of peers.
		if offset >= 0 {
			replCounter.replEntries[offset].bitVec.Set(confirm.peerId)
		}
		replCounter.RUnlock()
		
		go ld.marchCommit(&confirm)
	}
}

func (ld *Leader) log(format string, args ...interface{}) {
	log.Printf("[Leader %d/%d/%d] %s\n", ld.rf.me, ld.term, ld.rf.logs.lastCommitedIndex.Load(), fmt.Sprintf(format, args...))
}

func newReplCounter(majorN int, peerNum int) *ReplCounter {
	return &ReplCounter {
		majorN: majorN,
		peerNum: peerNum,
	}
}

func (ld *Leader) watchEntries(entryIndices ...int) {
	rc := ld.replCounter
	for _, idx := range entryIndices {
		bitVec := newBitVec(rc.peerNum)
		bitVec.Set(ld.rf.me)
		rc.replEntries = append(rc.replEntries, ReplEntry {
			bitVec: bitVec,
			index: idx,
		})
	}
}


func (ld *Leader) marchCommit(confirm *ReplConfirm) {
	rc := ld.replCounter
	rc.Lock()
	defer rc.Unlock()
	
	ld.log("Try marchCommit for confirm of %d", confirm.index)
	
	if confirm.index != 
		int(ld.rf.logs.lastCommitedIndex.Load()+1) { 
		ld.log("Confirm index %d != last commit index %d+1, return", confirm.index, ld.rf.logs.lastCommitedIndex.Load())
		return 
	}

	ld.log("MarchCommit for confirm of %d", confirm.index)

	i, n := 0, len(rc.replEntries)
	
	for i < n {
		ld.log("Confirm count of log %d = %d", rc.replEntries[i].index, rc.replEntries[i].bitVec.Count())
		if rc.replEntries[i].bitVec.Count() <= rc.majorN {
			break
		}
		i++
	}
	
	if i > 0 {
		i-- // cause i is the smallest that is not replicated to majority.
		newCommitIndex := rc.replEntries[i].index
		ld.log("March last commit index to %d", newCommitIndex)
		ld.rf.logs.lastCommitedIndex.Store(int32(newCommitIndex))
		go ld.rf.applyLogs()

		rc.replEntries = rc.replEntries[i:]
	}
}
