// Date:   Mon Apr 15 17:35:01 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import "sync/atomic"

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

type Applier struct {
	applyCh chan ApplyMsg
	count int
}

type Logs struct {
	entries []*LogEntry
	lci atomic.Int32
	lli atomic.Int32
	lai atomic.Int32
	noopCount int
	rf *Raft
}

func newLogs(rf *Raft, persistLogs *PersistentLogs) *Logs {
	logs := &Logs {
		rf: rf,
	}
	if persistLogs == nil {
		logs.lci.Store(-1)
		logs.lli.Store(-1)
		logs.lai.Store(-1)
	} else {
		logs.lli.Store(persistLogs.Lli)
		logs.lci.Store(persistLogs.Lci)
		logs.lai.Store(persistLogs.Lai)
		logs.entries = persistLogs.Entries
		logs.noopCount = persistLogs.NoopCount
	}
	return logs
}

func (logs *Logs) updateCommit(leaderCommit int) {
	if leaderCommit <= logs.LCI() {
		return
	}

	newCommit := minInt(leaderCommit, logs.LLI())
	if newCommit > logs.LCI() {
		logs.lci.Store(int32(newCommit))
		logs.rf.log("Update LCI to %d", newCommit)
		logs.rf.save()
	
		go func(lci int) {
			lai := logs.LAI()
			assert(lai <= lci)
			for _, et := range logs.entries[lai+1:lci+1] {
				switch et.CommandIndex {
				case NOOP_INDEX:
					
				default:
					logs.rf.applyCh <- ApplyMsg {
						CommandValid: true,
						CommandIndex: et.CommandIndex,
						Command: et.Content,
					}
					logs.rf.log("Applied log %d, content = %d", et.CommandIndex, et.Content.(int))
				}
			}

			logs.lai.Store(int32(lci))
		}(logs.LCI())
	}
}

func (logs *Logs) indexLogTerm(idx int) int {
	if idx < 0 || idx > logs.LLI() {
		return -1
	} else {
		return logs.entries[idx].Term
	}
}

func (logs *Logs) indexLogEntry(idx int) *LogEntry {
	if idx < 0 || idx > logs.LLI() {
		return nil
	} else {
		return logs.entries[idx]
	}
}

func (logs *Logs) pushEntry(et *LogEntry) int {
	logs.entries = append(logs.entries, et)
	logs.lli.Store(int32(len(logs.entries)-1))

	switch et.CommandIndex {
	case NOOP_INDEX:
		logs.noopCount++

	default:
		et.CommandIndex = logs.LLI() - logs.noopCount + 1
	}

	return et.CommandIndex
}

func (logs *Logs) followerAppendEntry(et *LogEntry, prev LogInfo) bool {
	// this is an old leader, it already committed this log entry, 
	// but it crashed before it told everyone.
	if prev.Index < int(logs.lci.Load()) {
		logs.rf.log("Received an entry index %d < LCI %d", prev.Index, logs.LCI())
		return true
	}

	lli := logs.LLI()
	prevMatch := false
	switch {
	case prev.Index < -1:
		logs.rf.fatal("Illegal prev index = %d", prev.Index)
	// prev out of bounds
	case prev.Index > lli:
		prevMatch = false

	// prev matches
	case prev.Index < 0 || logs.indexLogTerm(prev.Index) == prev.Term:
		prevMatch = true

		if et != nil {
			switch {
			case prev.Index == lli:
				logs.pushEntry(et)

			case logs.indexLogTerm(prev.Index+1) != et.Term:
				logs.removeAfter(prev.Index)
				logs.pushEntry(et)
			}

			logs.rf.save()
		}
	}

	return prevMatch
}

// Leader appends an entry, return the last log index, 
// and the last user command log index.
func (logs *Logs) leaderAppendEntry(et *LogEntry) (int, int) {
	assert(et != nil)

	commandIndex := logs.pushEntry(et)
	logs.rf.save()
	lli := logs.LLI()
	return lli, commandIndex
}

func (logs *Logs) removeAfter(idx int) {
	for _, et := range logs.entries[idx+1:] {
		if et.CommandIndex == NOOP_INDEX {
			logs.noopCount--
		}
	}
	logs.entries = logs.entries[:idx+1]
	logs.lli.Store(int32(len(logs.entries)-1))
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
	l := len(logs.entries)
	if l == 0 { return true }

	let := logs.entries[l-1] // the last entry

	if let.Term == lg.Term {
		return l-1 <= lg.Index
	} else {
		return let.Term < lg.Term
	}
}

func (logs *Logs) lastLogInfo() LogInfo {
	if len(logs.entries) == 0 {
		return LogInfo { -1, 0 }
	} else {
		lli := logs.LLI()
		return LogInfo {
			Index: lli,
			Term: logs.entries[lli].Term,
		}
	}
}
