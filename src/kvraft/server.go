// Date:   Wed May 15 13:39:03 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package kvraft

import (
	"6.5840/labgob"
	"6.5840/labrpc"
	"6.5840/raft"
	"log"
	"sync"
	"sync/atomic"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

type SyncInfo struct {
	lastRpcID int
	lastValue string
}

type KVServer struct {
	mu      sync.Mutex
	me      int
	rf      *raft.Raft
	applyCh chan raft.ApplyMsg
	dead atomic.Bool

	maxraftstate int // snapshot if log grows this big

	// Your definitions here.
	syncInfo sync.Map

	startedCmds []CmdInfo

	sync.Map
}

func (kv *KVServer) checkSync(clientID int, rpcID int, reply *RpcReply) bool {
	actual, loaded := kv.syncInfo.LoadOrStore(clientID, &SyncInfo {
		lastRpcID: 1,
	})
	// sync at start.
	if !loaded { return true }

	if actual.(*SyncInfo).lastRpcID != rpcID {
		reply.RpcID = actual.(*SyncInfo).lastRpcID-1
		reply.OpRes = OP_UNSYNC
		return false
	} else {
		return true
	}
}

func (kv *KVServer) startOp(args *RpcArgs, reply *RpcReply, op Op) {
	// Your code here.
	if !kv.checkSync(args.ClientID, args.RpcID, reply) {
		return
	}

	kv.mu.Lock()

	cmd := &CmdEntry {
		clientID: args.ClientID,
		rpcID: args.RpcID,
		op: op,
	}

	index, _, ok := kv.rf.Start(cmd)
	if !ok {
		reply.OpRes = OP_FAIL
		kv.mu.Unlock()
		return
	}

	ch := make(chan bool, 1)
	cmdInfo := CmdInfo {
		cmdIndex: index,
		clientID: args.ClientID,
		rpcID: args.RpcID,
		ch: ch,
		reply: reply,
	}
	kv.startedCmds = append(kv.startedCmds, cmdInfo)

	kv.mu.Unlock()
	<- ch

}

func (kv *KVServer) Get(args *RpcArgs, reply *RpcReply) {
	kv.startOp(args, reply, &GetOp{})
}

func (kv *KVServer) Put(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
}

func (kv *KVServer) Append(args *PutAppendArgs, reply *PutAppendReply) {
	// Your code here.
}

// RPC call
func (kv *KVServer) GetState(args interface{}, reply *GetStateReply) {
	reply.Term, reply.IsLeader = kv.rf.GetState()
}

// the tester calls Kill() when a KVServer instance won't
// be needed again. for your convenience, we supply
// code to set rf.dead (without needing a lock),
// and a killed() method to test rf.dead in
// long-running loops. you can also add your own
// code to Kill(). you're not required to do anything
// about this, but it may be convenient (for example)
// to suppress debug output from a Kill()ed instance.
func (kv *KVServer) Kill() {
	kv.dead.Store(true)
	kv.rf.Kill()
	// Your code here, if desired.
}

func (kv *KVServer) killed() bool {
	return kv.dead.Load()
}

// servers[] contains the ports of the set of
// servers that will cooperate via Raft to
// form the fault-tolerant key/value service.
// me is the index of the current server in servers[].
// the k/v server should store snapshots through the underlying Raft
// implementation, which should call persister.SaveStateAndSnapshot() to
// atomically save the Raft state along with the snapshot.
// the k/v server should snapshot when Raft's saved state exceeds maxraftstate bytes,
// in order to allow Raft to garbage-collect its log. if maxraftstate is -1,
// you don't need to snapshot.
// StartKVServer() must return quickly, so it should start goroutines
// for any long-running work.
func StartKVServer(servers []*labrpc.ClientEnd, me int, persister *raft.Persister, maxraftstate int) *KVServer {
	// call labgob.Register on structures you want
	// Go's RPC library to marshall/unmarshall.
	labgob.Register(&GetOp{})
	labgob.Register(&PutOp{})
	labgob.Register(&AppendOp{})
	labgob.Register(&RpcArgs{})

	kv := new(KVServer)
	kv.me = me
	kv.maxraftstate = maxraftstate

	// You may need initialization code here.

	kv.applyCh = make(chan raft.ApplyMsg)
	kv.rf = raft.Make(servers, me, persister, kv.applyCh)

	// You may need initialization code here.

	go kv.apply()

	return kv
}
