// Date:   Mon Apr 29 17:02:49 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"time"

	"6.5840/labrpc"
)

type Replicator struct {
	peerId int
	peer *labrpc.ClientEnd
	ld *Leader
	nextIndex int
}

type ReplEntry struct {
	bitVec *BitVec
	index int
}

type ReplCounter struct {
	rf *Raft
	entries []*ReplEntry
	majorN int
	size int
}

func newReplicator(peer *labrpc.ClientEnd, id int, ld *Leader) *Replicator {
	return &Replicator {
		peerId: id,
		peer: peer,
		ld: ld,
		nextIndex: ld.rf.logs.LCI() + 1,
	}
}

func (repl *Replicator) start() {
	ld := repl.ld
	logs := ld.rf.logs
	
	for ld.active.Load() {
		assert(repl.nextIndex >= 0)

		if repl.nextIndex > logs.LLI() {
			time.Sleep(NEW_LOG_CHECK_FREQ)
			continue
		}

		round: for ld.active.Load() {
			prevIndex := repl.nextIndex - 1
			args := &AppendEntriesArgs {
				Id: ld.rf.me,
				Term: ld.term,
				PrevLogTerm: logs.indexLogTerm(prevIndex),
				PrevLogIndex: prevIndex,
				LeaderCommit: logs.LCI(),
				Entry: logs.indexLogEntry(repl.nextIndex),
			}
			reply := &AppendEntriesReply {}

			for ok := repl.peer.Call("Raft.AppendEntries", args, reply);
			!ok || !reply.Responsed; 
			ok = repl.peer.Call("Raft.AppendEntries", args, reply) {}

			switch reply.EntryStatus {
			case ENTRY_DEFAULT:
				ld.fatal("AppendEntries RPC EntryStatus is default from %d", repl.peerId)
				
			case ENTRY_STALE:
				ld.log("%d said i'm stale", repl.peerId)
				ld.rf.tryPutEv(&StaleLeaderEvent { reply.Term }, ld)
				return

			case ENTRY_FAILURE:
				repl.nextIndex--
				ld.log("%d nextIndex fallback to %d", repl.peerId, repl.nextIndex)
				break round

			case ENTRY_HOLD:
				time.Sleep(APPEND_WAITING)
				continue round

			case ENTRY_SUCCESS:
				ld.log("%d confirmed log %d", repl.peerId, repl.nextIndex)
				ld.rf.tryPutEv(&ReplConfirmEvent { repl.peerId, repl.nextIndex }, ld)
				repl.nextIndex++
				break round
			}
		}
	}
}

func newReplCounter(rf *Raft) *ReplCounter {
	return &ReplCounter {
		rf: rf,
	}
}

func (rc *ReplCounter) watchIndex(idx int) {
	assert(idx == rc.entries[len(rc.entries)-1].index+1)
	bitVec := newBitVec(rc.size)
	bitVec.Set(rc.rf.me)
	rc.entries = append(rc.entries, &ReplEntry {
		bitVec: bitVec,
		index: idx,
	})
}

func (rc *ReplCounter) confirm(idx int, peerId int) {
	baseIndex := rc.entries[0].index
	offset := idx - baseIndex
	if offset < 0 { return }
	assert(offset < len(rc.entries))
	rc.entries[offset].bitVec.Set(peerId)

	i, n := 0, len(rc.entries)
	for ; i < n && rc.entries[i].bitVec.Count() >= rc.rf.majorN; i++ {
		rc.rf.applyCh <- ApplyMsg {
			CommandValid: true,
			CommandIndex: rc.entries[i].index,
			Command: rc.rf.logs.entries[i].Content,
		}
		rc.rf.log("Applied log %d", rc.entries[i].index)
	}

	if i > 0 {
		rc.rf.log("Update LCI to %d", rc.entries[i-1].index)
		rc.rf.logs.updateCommit(rc.entries[i-1].index)
	}
	rc.entries = rc.entries[i:]
}
