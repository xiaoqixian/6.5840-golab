// Date:   Fri May 03 12:48:36 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package raft

import (
	"bytes"
	"encoding/gob"
	"log"
)

type RolePersist interface {}

type PersistentLogs struct {
	Entries []LogEntry
	Lci int32
	Lli int32
	Lai int32
	NoopCount int
	Offset int
	
	Snapshot *Snapshot
}

type PersistentState struct {
	VoteFor int
	Term int
	Logs PersistentLogs
}

// this function should be called when 
// the Logs is locked.
func (rf *Raft) encodeState() []byte {
	rf.log("Encode state, LAI = %d", rf.logs.lai.Load())
	logs := rf.logs
	state := PersistentState { 
		VoteFor: rf.voteFor,
		Term: rf.term,
		Logs: PersistentLogs {
			Entries: logs.entries,
			Lci: logs.lci.Load(),
			Lli: logs.lli.Load(),
			Lai: logs.lai.Load(),
			NoopCount: logs.noopCount,
			Offset: logs.offset,
		},
	}
	if logs.snapshot != nil {
		state.Logs.Snapshot = &Snapshot {
			Snapshot: nil,
			LastIncludeIndex: logs.snapshot.LastIncludeIndex,
			LastIncludeTerm: logs.snapshot.LastIncludeTerm,
			LastIncludeCmdIndex: logs.snapshot.LastIncludeCmdIndex,
			NoopCount: logs.snapshot.NoopCount,
		}
	}

	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(state)
	if err != nil {
		log.Fatal(err.Error())
	}
	return buf.Bytes()
}


func (rf *Raft) persistState() {
	// if !rf.saveLock.TryLock() { return }
	rf.saveLock.Lock()
	defer rf.saveLock.Unlock()

	rf.persister.SaveState(rf.encodeState())
}

func (rf *Raft) persist() {
	rf.saveLock.Lock()
	defer rf.saveLock.Unlock()

	if rf.logs.snapshot != nil {
		rf.persister.Save(rf.encodeState(), rf.logs.snapshot.Snapshot)
	} else {
		rf.persister.Save(rf.encodeState(), nil)
	}
}
