// Date:   Mon Apr 15 17:35:01 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"sync"
	"sync/atomic"
)

const (
	NOOP_INDEX int = -1
)

type LogEntry struct {
	CommandIndex int // -1 represents Noop Entry.
	Term int
	Content interface{}
}
type LogInfo struct {
	Index int
	Term int
}

type ApplyEntry interface {}

type ApplyLogEntry struct {
	applyEntries []LogEntry
	// for updating LAI after all entries applied
	lai int
}
type ApplySnapshotEntry struct {
	snapshot *Snapshot
}

type Logs struct {
	entries []LogEntry
	lci atomic.Int32
	lli atomic.Int32
	lai atomic.Int32
	noopCount int
	rf *Raft
	offset int

	applier chan ApplyEntry

	snapshot *Snapshot

	sync.RWMutex
}

func newLogs(rf *Raft, persistLogs *PersistentLogs, snapshot *Snapshot) *Logs {
	logs := &Logs {
		rf: rf,
		offset: 0,
		applier: make(chan ApplyEntry, 10),
		snapshot: nil,
	}
	if persistLogs == nil {
		assert(snapshot == nil)
		logs.lci.Store(-1)
		logs.lli.Store(-1)
		logs.lai.Store(-1)
	} else {
		logs.lli.Store(persistLogs.Lli)
		logs.lci.Store(persistLogs.Lci)
		logs.lai.Store(persistLogs.Lai)
		logs.entries = persistLogs.Entries
		logs.noopCount = persistLogs.NoopCount
		logs.snapshot = snapshot
	}

	go func() {
		for !logs.rf.dead.Load() {
			aet := <- logs.applier
			
			switch aet := aet.(type) {
			case *ApplyLogEntry:
				for _, et := range aet.applyEntries {
					switch et.CommandIndex {
					case NOOP_INDEX:
						
					default:
						logs.rf.applyCh <- ApplyMsg {
							CommandValid: true,
							CommandIndex: et.CommandIndex,
							Command: et.Content,
						}
						logs.rf.log("Applied log %d", et.CommandIndex)
					}
				}

				logs.lai.Store(int32(aet.lai))
				logs.rf.log("Update LAI to %d", aet.lai)

			case *ApplySnapshotEntry:
				logs.rf.applyCh <- ApplyMsg {
					SnapshotValid: true,
					Snapshot: aet.snapshot.Snapshot,
					SnapshotIndex: aet.snapshot.LastIncludeCmdIndex,
					SnapshotTerm: aet.snapshot.LastIncludeTerm,
				}
				logs.lai.Store(int32(aet.snapshot.LastIncludeIndex))

			default:
				logs.rf.fatal("Unknown ApplyEntry type: %s", typeName(aet))
			}
		}
	}()
	return logs
}

func (logs *Logs) updateCommit(leaderCommit int) {
	if leaderCommit <= logs.LCI() {
		return
	}

	newCommit := minInt(leaderCommit, logs.LLI())
	if newCommit > logs.LCI() {
		st, ed := logs.LAI()-logs.offset+1, newCommit-logs.offset+1
		logs.applier <- &ApplyLogEntry {
			applyEntries: logs.entries[st:ed],
			lai: newCommit,
		}

		logs.lci.Store(int32(newCommit))
		// assume these entries can be successfully applied.
		logs.rf.log("Update LCI to %d", newCommit)
		logs.rf.persistState()
	}
}

// return 0, false if the index is less than LII.
func (logs *Logs) tryIndexLogTerm(idx int) (int, bool) {
	logs.RLock()
	defer logs.RUnlock()

	switch {
	case logs.snapshot != nil && idx == logs.snapshot.LastIncludeIndex:
		return logs.snapshot.LastIncludeTerm, true

	case idx < logs.offset || idx > logs.LLI():
		return 0, false

	}
	return logs.entries[idx - logs.offset].Term, true
}

func (logs *Logs) indexLogTermNoLock(idx int) int {
	switch {
	case idx < 0 || idx > logs.LLI():
		return -1
	case logs.snapshot != nil && idx < logs.snapshot.LastIncludeIndex:
		logs.rf.fatal("index log term less than snapshot last log index: %d < %d", idx, logs.snapshot.LastIncludeIndex)
	case logs.snapshot != nil && idx == logs.snapshot.LastIncludeIndex:
		return logs.snapshot.LastIncludeTerm

	default:
		return logs.entries[idx - logs.offset].Term
	}

	// should not reach here.
	assert(false)
	return -1
}

func (logs *Logs) indexLogTerm(idx int) int {
	logs.RLock()
	defer logs.RUnlock()
	return logs.indexLogTermNoLock(idx)
}

func (logs *Logs) pushEntry(et LogEntry) int {
	newLLI := logs.LLI() + 1
	switch et.CommandIndex {
	case NOOP_INDEX:
		logs.noopCount++

	default:
		logs.rf.log("logs.noopCount = %d", logs.noopCount)
		et.CommandIndex = newLLI - logs.noopCount + 1
	}

	logs.entries = append(logs.entries, et)
	logs.lli.Store(int32(newLLI))

	return et.CommandIndex
}

func (logs *Logs) pushEntries(ets []LogEntry) {
	for _, et := range ets {
		switch et.CommandIndex {
		case NOOP_INDEX:
			logs.noopCount++
		}
	}

	logs.entries = append(logs.entries, ets...)
	logs.lli.Store(int32(len(logs.entries)-1 + logs.offset))
}

func (logs *Logs) followerAppendEntries(args *SendEntries) EntryStatus {
	assert(args != nil)
	prev, ets := args.PrevLogInfo, args.Entries
	if prev.Index < logs.LCI() {
		// assert(logs.entries[prev.Index+1].Term == ets[0].Term)
		logs.rf.log("Received an entry index %d < LCI %d", prev.Index, logs.LCI())
		return ENTRY_MATCH
	}

	lli := logs.LLI()
	switch {
	case prev.Index < -1:
		logs.rf.fatal("Illegal prev index = %d", prev.Index)
	// prev out of bounds
	case prev.Index > lli:
		return ENTRY_UNMATCH

	case prev.Index < 0 || logs.indexLogTermNoLock(prev.Index) == prev.Term:
		if len(ets) > 0 {
			if prev.Index < lli {
				logs.removeAfter(prev.Index)
			}
			logs.pushEntries(ets)
			logs.rf.persistState()
		}
		return ENTRY_MATCH
	}
	return ENTRY_UNMATCH
}

func (logs *Logs) followerInstallSnapshot(snapshot *Snapshot) {
	logs.Lock()
	defer logs.Unlock()

	logs.snapshot = snapshot

	logs.entries = []LogEntry{}
	logs.offset = snapshot.LastIncludeIndex+1
	logs.noopCount = snapshot.NoopCount
	logs.lli.Store(int32(snapshot.LastIncludeIndex))
	logs.lci.Store(int32(snapshot.LastIncludeIndex))
	logs.applier <- &ApplySnapshotEntry { snapshot }

	logs.rf.persist()
}

// Leader appends an entry, return the last log index, 
// and the last user command log index.
func (logs *Logs) leaderAppendEntry(et LogEntry) (int, int) {
	commandIndex := logs.pushEntry(et)
	logs.rf.persistState()
	return logs.LLI(), commandIndex
}

// idx not included
func (logs *Logs) removeAfter(idx int) {
	for _, et := range logs.entries[idx+1-logs.offset:] {
		if et.CommandIndex == NOOP_INDEX {
			logs.noopCount--
		}
	}
	logs.entries = logs.entries[:idx+1-logs.offset]
	logs.lli.Store(int32(len(logs.entries)-1+logs.offset))
}

// idx included
func (logs *Logs) entriesAfter(idx int) (*Snapshot, *SendEntries) {
	logs.RLock()
	defer logs.RUnlock()
	
	if idx < logs.offset {
		return logs.snapshot, nil
	}

	return nil, &SendEntries {
		Entries: logs.entries[idx-logs.offset:],
		PrevLogInfo: LogInfo {
			Index: idx-1,
			Term: logs.indexLogTerm(idx-1),
		},
	}
}

func (logs *Logs) takeSnapshot(cmdIndex int, snapshot []byte) {
	// find the log index according to the command index.
	// goroutines only read entries at this point, so 
	// we don't have to acquire the write lock.
	logIndex, noopCount := 0, 0
	search: for i := 0; i < len(logs.entries); i++ {
		switch logs.entries[i].CommandIndex {
		case NOOP_INDEX:
			noopCount++
		case cmdIndex:
			logIndex = i + logs.offset
			break search
		}
	}
	assert(logIndex < len(logs.entries) + logs.offset) // assert the log index can be found
	assert(logs.entries[logIndex-logs.offset].CommandIndex == cmdIndex)
	logs.rf.log("Found logIndex = %d", logIndex)

	if logs.snapshot != nil {
		noopCount += logs.snapshot.NoopCount
	}

	logs.Lock()
	defer logs.Unlock()

	logs.snapshot = &Snapshot {
		Snapshot: snapshot,
		LastIncludeIndex: logIndex,
		LastIncludeTerm: logs.indexLogTermNoLock(logIndex),
		LastIncludeCmdIndex: cmdIndex,
		NoopCount: noopCount,
	}

	logs.entries = logs.entries[logIndex-logs.offset+1:]
	logs.offset = logIndex + 1

	logs.rf.persist()
}

func (logs *Logs) LCI() int {
	return int(logs.lci.Load())
}

func (logs *Logs) LLI() int {
	return int(logs.lli.Load())
}

func (logs *Logs) LAI() int {
	return int(logs.lai.Load())
}

// compare if the log is as up-to-date the logs' last log.
// Raft determines which of two logs is more up-to-date
// by comparing the index and term of the last entries in the
// logs. If the logs have last entries with different terms, then
// the log with the later term is more up-to-date. If the logs
// end with the same term, then whichever log is longer is
// more up-to-date.
func (logs *Logs) atLeastUpToDate(lg LogInfo) bool {
	myLast := logs.lastLogInfo()

	if myLast.Term == lg.Term {
		return myLast.Index <= lg.Index
	} else {
		return myLast.Term < lg.Term
	}
}

func (logs *Logs) lastLogInfo() LogInfo {
	logs.RLock()
	defer logs.RUnlock()

	if len(logs.entries) == 0 {
		if logs.snapshot == nil {
			return LogInfo { -1, 0 }
		} else {
			return LogInfo {
				Index: logs.snapshot.LastIncludeIndex,
				Term: logs.snapshot.LastIncludeTerm,
			}
		}
	} else {
		lli := logs.LLI()
		return LogInfo {
			Index: lli,
			Term: logs.entries[lli-logs.offset].Term,
		}
	}
}
