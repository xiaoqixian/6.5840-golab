// Date:   Mon Apr 15 17:35:01 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import "sync/atomic"

type EntryType uint8
const (
	ENTRY_NORMAL EntryType = iota
	ENTRY_NOOP
)

type LogEntry struct {
	Type EntryType
	Term int
	Content interface{}
}
type PrevLogInfo struct {
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
	lai int
	noopCount int
	rf *Raft
	applier *Applier
}

func newLogs(rf *Raft, applyCh chan ApplyMsg) *Logs {
	logs := &Logs {
		rf: rf,
		applier: &Applier {
			count: 0,
			applyCh: applyCh,
		},
		lai: -1,
	}
	logs.lci.Store(-1)
	logs.lli.Store(-1)
	return logs
}

func (logs *Logs) updateCommit(leaderCommit int) {
	if leaderCommit <= logs.LCI() {
		return
	}

	newCommit := minInt(leaderCommit, int(logs.lli.Load()))
	if newCommit > logs.LCI() {
		logs.lci.Store(int32(newCommit))
		logs.rf.log("Update LCI to %d", newCommit)
	
		go func(lci int) {
			lai := logs.lai
			assert(lai <= lci)
			for i := lai+1; i <= lci; i++ {
				logs.rf.applyCh <- ApplyMsg {
					CommandValid: true,
					CommandIndex: i,
					Command: logs.entries[i].Content,
				}
				logs.rf.log("Applied log %d", i)
			}

			logs.lai = lci
			logs.rf.log("Update LAI to %d", lci)
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

func (logs *Logs) followerAppendEntry(et *LogEntry, prev PrevLogInfo) bool {
	assert(prev.Index >= int(logs.lci.Load()))

	if prev.Index > logs.LLI() {
		return false
	}

	myPrevTerm := logs.indexLogTerm(prev.Index)

	if myPrevTerm == prev.Term {
		logs.entries = logs.entries[:prev.Index+1]
		logs.entries = append(logs.entries, et)
		logs.lli.Store(int32(len(logs.entries)-1))
		return true
	}
	return false
}

func (logs *Logs) leaderAppendEntry(et *LogEntry) (int, int) {
	logs.entries = append(logs.entries, et)
	logs.lli.Store(int32(len(logs.entries)-1))

	switch et.Type {
	case ENTRY_NOOP:
		logs.noopCount++
		return len(logs.entries)-1, -1
		
	case ENTRY_NORMAL:
		return len(logs.entries)-1, len(logs.entries)-logs.noopCount
	}
	return -1, -1
}

func (logs *Logs) removeUncommittedTail() {
	logs.entries = logs.entries[:logs.lci.Load()+1]
	logs.lli.Store(int32(len(logs.entries)-1))
}

func (logs *Logs) LCI() int {
	return int(logs.lci.Load())
}

func (logs *Logs) LLI() int {
	return int(logs.lli.Load())
}
