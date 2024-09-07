// Date:   Wed May 15 17:45:53 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package kvraft

import (
	"sync"

	"6.5840/raft"
)

type Value struct {
	value string
	sync.RWMutex
}

func (kv *KVServer) get(key string) string {
	val, ok := kv.Load(key)
	if !ok {
		return ""
	}
	value := val.(*Value)
	value.RLock()
	defer value.RUnlock()
	return value.value
}

func (kv *KVServer) put(key string, value string) {
	kv.Store(key, value)
}

func (kv *KVServer) append(key string, value string) string {
	actual, loaded := kv.LoadOrStore(key, value)
	if !loaded { return "" }

	val := actual.(*Value)
	val.RLock()
	defer val.RUnlock()
	old := val.value
	val.value += value
	return old
}

func (kv *KVServer) installSnapshot(applyMsg raft.ApplyMsg) {

}

func (kv *KVServer) checkMyStarted(applyMsg *raft.ApplyMsg) *CmdInfo {
	kv.mu.Lock()
	defer kv.mu.Unlock()
	
	if len(kv.startedCmds) == 0 { return nil }

	cmdEntry := applyMsg.Command.(*CmdEntry)
	cmdInfo := &kv.startedCmds[0]

	assert(cmdInfo.cmdIndex >= applyMsg.CommandIndex)

	if (cmdInfo.cmdIndex > applyMsg.CommandIndex) {
		return nil
	}

	kv.startedCmds = kv.startedCmds[1:]
	if (cmdInfo.clientID == cmdEntry.clientID && 
		cmdInfo.rpcID == cmdEntry.rpcID) {
		return cmdInfo
	} else {
		cmdInfo.reply.OpRes = OP_FAIL
		cmdInfo.ch <- false
		return nil
	}
}

// A goroutine function
func (kv *KVServer) apply() {
	for !kv.dead.Load() {
		applyMsg := <- kv.applyCh
		
		if applyMsg.SnapshotValid {
			kv.installSnapshot(applyMsg)
			continue
		}

		assert(applyMsg.CommandValid)

		if applyMsg.CommandIndex == raft.NOOP_INDEX { continue }

		cmdEntry := applyMsg.Command.(*CmdEntry)
		cmdInfo := kv.checkMyStarted(&applyMsg)

		switch op := cmdEntry.op.(type) {
		case *GetOp:
			if cmdInfo != nil {
				cmdInfo.reply.OpRes = OP_SUCCESS
				cmdInfo.reply.OpReply = 
					kv.get(cmdInfo.args.OpArgs.(*GetArgs).Key)

				cmdInfo.ch <- true
			}

		case *PutOp:
			kv.put(op.Key, op.Value)
			if cmdInfo != nil {
				cmdInfo.reply.OpRes = OP_SUCCESS
				cmdInfo.ch <- true
			}
		case *AppendOp:
			old := kv.append(op.Key, op.Value)
			if cmdInfo != nil {
				cmdInfo.reply.OpRes = OP_SUCCESS
				cmdInfo.reply.OpReply = old
				cmdInfo.ch <- true
			}
		}
	}
}
