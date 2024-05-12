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
		nextIndex: 0,
	}
}

func (repl *Replicator) call(args *AppendEntriesArgs) *AppendEntriesReply {
	reply := &AppendEntriesReply {}
	f := func() bool {
		return repl.peer.Call("Raft.AppendEntries", args, reply)
	}
	for ok := rpcMultiTry(f); 
	(!ok || !reply.Responsed) && repl.ld.active.Load();
	ok = rpcMultiTry(f) {
		time.Sleep(RPC_FAIL_WAITING)
	}
	return reply
}

func (repl *Replicator) matchIndex() {
	ld := repl.ld
	logs := ld.rf.logs

	lii := func() int {
		logs.RLock()
		defer logs.RUnlock()
		if logs.snapshot != nil {
			return logs.snapshot.LastIncludeIndex
		}
		return 0
	}

	prevLogIndex := -1
	
	search: for ld.active.Load() {
		l, r := lii(), logs.LLI()
		for ld.active.Load() && l <= r {
			m := l + (r - l)/2
			mTerm, ok := logs.tryIndexLogTerm(m)
			ld.log("tryIndexLogTerm, m = %d, mTerm = %d, ok = %t", m, mTerm, ok)
			if !ok { continue search }

			args := &AppendEntriesArgs {
				Id: ld.rf.me,
				Term: ld.rf.term,
				LeaderCommit: logs.LCI(),
				SendEntries: &SendEntries {
					Entries: nil,
					PrevLogInfo: LogInfo {m, mTerm}, 
				},
				EntryType: ENTRY_T_QUERY,
				Snapshot: nil,
			}
			round: for ld.active.Load() {
				reply := repl.call(args)
				if !repl.ld.active.Load() { return }

				switch reply.EntryStatus {
				case ENTRY_MATCH:
					prevLogIndex = m
					l = m + 1
					ld.log("[repl %d] m = %d matched", repl.peerId, m)
					break round

				case ENTRY_UNMATCH:
					ld.log("[repl %d] m = %d not matched", repl.peerId, m)
					r = m - 1
					break round

				case ENTRY_STALE:
					ld.rf.transRole(followerFromLeader)
					return
					
				case ENTRY_HOLD:
					
				default:
					ld.fatal("Unprocessed EntryStatus: %d", reply.EntryStatus)
				}
			}
		}
		break search
	}
	if ld.active.Load() {
		repl.nextIndex = prevLogIndex + 1
		ld.log("Peer %d match index = %d", repl.peerId, repl.nextIndex)
	}
}

func (repl *Replicator) start() {
	repl.matchIndex()

	ld := repl.ld
	logs := ld.rf.logs

	if !ld.active.Load() { return }

	hbTicker := time.NewTicker(HEARTBEAT_SEND)
	defer hbTicker.Stop()
	
	replication: for ld.active.Load() {
		assert(repl.nextIndex >= 0)

		<- hbTicker.C
		logs.rf.log("Replication to peer %d", repl.peerId)
		
		snapshot, sendEntries := logs.entriesAfter(repl.nextIndex)

		args := &AppendEntriesArgs {
			Id: ld.rf.me,
			Term: ld.rf.term,
			SendEntries: sendEntries,
			LeaderCommit: logs.LCI(),
			EntryType: ENTRY_T_LOG,
			Snapshot: snapshot,
		}

		round: for ld.active.Load() {
			args.LeaderCommit = logs.LCI()
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

			case ENTRY_UNMATCH:
				ld.log("Peer %d lost match, rematch", repl.peerId)
				repl.matchIndex()
				continue replication

			case ENTRY_HOLD:
				time.Sleep(HOLD_WAITING)
				continue round

			case ENTRY_MATCH:
				if snapshot != nil {
					ld.log("Peer %d confirmed snapshot with lastIncludeIndex = %d", repl.peerId, snapshot.LastIncludeIndex)

					repl.nextIndex = snapshot.LastIncludeIndex + 1
				} else if len(sendEntries.Entries) > 0 {
					ld.log("%d confirmed log [%d %d]", repl.peerId, repl.nextIndex, repl.nextIndex+len(sendEntries.Entries))

					ld.rf.tryPutEv(&ReplConfirmEvent {
						id: repl.peerId,
						startIndex: repl.nextIndex,
						endIndex: repl.nextIndex + len(sendEntries.Entries),
					}, ld)
							
					repl.nextIndex += len(sendEntries.Entries)
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

func (rc *ReplCounter) confirm(peerId int, startIdx int, endIdx int) {
	rc.rf.log("Peer %d confirmed log [%d, %d)", peerId, startIdx, endIdx)

	if len(rc.entries) == 0 { return } // redundant confirm

	baseIndex := rc.entries[0].index
	endOffset := endIdx - baseIndex
	assert(endOffset <= len(rc.entries))

	for offset := maxInt(startIdx - baseIndex, 0); 
		offset < endOffset; offset++ {
		rc.entries[offset].bitVec.Set(peerId)
	}

	i, n := 0, len(rc.entries)
	for ; i < n && rc.entries[i].bitVec.Count() >= rc.rf.majorN; i++ {}

	if i > 0 {
		rc.rf.log("Update LCI to %d", rc.entries[i-1].index)
		rc.rf.logs.updateCommit(rc.entries[i-1].index)
		rc.entries = rc.entries[i:]
	}
}
