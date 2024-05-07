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
	Entries []*LogEntry
	Lci int32
	Lli int32
	Lai int32
	NoopCount int
}

type PersistentState struct {
	Term int
	Logs PersistentLogs
}

func (rf *Raft) save() {
	if !rf.saveLock.TryLock() { return }
	defer rf.saveLock.Unlock()

	logs := rf.logs
	state := PersistentState { 
		Term: rf.term,
		Logs: PersistentLogs {
			Entries: logs.entries,
			Lci: logs.lci.Load(),
			Lli: logs.lli.Load(),
			Lai: logs.lai.Load(),
			NoopCount: logs.noopCount,
		},
	}

	buf := new(bytes.Buffer)
	enc := gob.NewEncoder(buf)
	err := enc.Encode(state)
	if err != nil {
		log.Fatal(err.Error())
	}

	rf.persister.Save(buf.Bytes(), nil)
}
