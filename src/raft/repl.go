// Date:   Mon Apr 29 17:02:49 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"time"

	"6.5840/labrpc"
)

type EntrySendType uint8
const (
	ENTRY_SEND_NOT_READY EntrySendType = iota
	ENTRY_SEND_HB
	ENTRY_SEND_NORMAL
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
}

func newReplicator(peer *labrpc.ClientEnd, id int, ld *Leader) *Replicator {
	return &Replicator {
		peerId: id,
		peer: peer,
		ld: ld,
		nextIndex: ld.rf.logs.LLI() + 1,
	}
}

func (repl *Replicator) start() {
	ld := repl.ld
	logs := ld.rf.logs

	hbTicker := time.NewTicker(HEARTBEAT_SEND)
	defer hbTicker.Stop()
	
	replication: for ld.active.Load() {
		assert(repl.nextIndex >= 0)

		sendType := ENTRY_SEND_NOT_READY

		select {
		case <- hbTicker.C:
			sendType = ENTRY_SEND_HB
		default:
		}
		if repl.nextIndex <= logs.LLI() {
			sendType = ENTRY_SEND_NORMAL
		}

		if sendType == ENTRY_SEND_NOT_READY {
			time.Sleep(NEW_LOG_CHECK_FREQ)
			continue replication
		}

		var sendEntry *LogEntry
		if sendType == ENTRY_SEND_NORMAL {
			sendEntry = logs.indexLogEntry(repl.nextIndex)
		}

		switch sendType {
		case ENTRY_SEND_HB:
			ld.log("Send a HeartBeat to %d", repl.peerId)
		case ENTRY_SEND_NORMAL:
			ld.log("Send an Entry to %d", repl.peerId)
		}

		round: for ld.active.Load() {
			args := &AppendEntriesArgs {
				Id: ld.rf.me,
				Term: ld.rf.term,
				PrevLogInfo: LogInfo {
					Index: repl.nextIndex-1,
					Term: logs.indexLogTerm(repl.nextIndex-1),
				},
				Entry: sendEntry,
				LeaderCommit: logs.LCI(),
			}
			reply := &AppendEntriesReply {}

			rpcCall := func() bool {
				return repl.peer.Call("Raft.AppendEntries", args, reply)
			}

			for ok := rpcMultiTry(rpcCall);
			(!ok || !reply.Responsed) && ld.active.Load(); 
			ok = rpcMultiTry(rpcCall) {
				time.Sleep(RPC_FAIL_WAITING)
				args.LeaderCommit = logs.LCI()
				ld.log("AppendEntries Call to peer %d try again", repl.peerId)
			}

			if !ld.active.Load() { break replication }

			switch reply.EntryStatus {
			case ENTRY_DEFAULT:
				ld.fatal("AppendEntries RPC EntryStatus is default from %d", repl.peerId)
				
			case ENTRY_STALE:
				ld.log("%d said i'm stale, my term = %d, reply term = %d", repl.peerId, ld.rf.term, reply.Term)
				ld.rf.tryPutEv(&StaleLeaderEvent { reply.Term }, ld)
				return

			case ENTRY_FAILURE:
				repl.nextIndex--
				ld.log("%d nextIndex fallback to %d", repl.peerId, repl.nextIndex)
				break round

			case ENTRY_HOLD:
				time.Sleep(HOLD_WAITING)
				continue round

			case ENTRY_SUCCESS:
				if sendEntry != nil {
					ld.log("%d confirmed log %d", repl.peerId, repl.nextIndex)
					ld.rf.tryPutEv(&ReplConfirmEvent { 
						repl.peerId, repl.nextIndex }, ld)
					repl.nextIndex++
				}
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
	assert(len(rc.entries) == 0 || idx == rc.entries[len(rc.entries)-1].index+1)
	bitVec := newBitVec(len(rc.rf.peers))
	bitVec.Set(rc.rf.me) // confirm for myself.
	rc.entries = append(rc.entries, &ReplEntry {
		bitVec: bitVec,
		index: idx,
	})
}

func (rc *ReplCounter) confirm(idx int, peerId int) {
	if len(rc.entries) == 0 { return } // redundant confirm
	baseIndex := rc.entries[0].index
	offset := idx - baseIndex
	if offset < 0 { return } // redundant confirm
	assert(offset < len(rc.entries))
	rc.entries[offset].bitVec.Set(peerId)

	i, n := 0, len(rc.entries)
	for ; i < n && rc.entries[i].bitVec.Count() >= rc.rf.majorN; i++ {}

	if i > 0 {
		rc.rf.log("Update LCI to %d", rc.entries[i-1].index)
		rc.rf.logs.updateCommit(rc.entries[i-1].index)
		rc.entries = rc.entries[i:]
	}
}
