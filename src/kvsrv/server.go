package kvsrv

import (
	"log"
	"sync"
)

const Debug = false

func DPrintf(format string, a ...interface{}) (n int, err error) {
	if Debug {
		log.Printf(format, a...)
	}
	return
}

type SyncInfo struct {
	RpcID uint32
	LastValue string // for Append RPC Reply
}

type KVServer struct {
	table sync.Map
	clientsSync sync.Map
}

type Value struct {
	value string
	sync.RWMutex
}

// sync client RPC ID, return a bool to indicate 
// should continue operation.
func (kv *KVServer) syncRpcID(args *PutAppendArgs, reply *PutAppendReply) bool {
	actual, _ := kv.clientsSync.LoadOrStore(args.ClientID, &SyncInfo { RpcID: 1 })
	clientSyncInfo := actual.(*SyncInfo)

	// the operation is already conducted.
	if clientSyncInfo.RpcID > args.RpcID {
		reply.RpcID = clientSyncInfo.RpcID - 1
		reply.Value = clientSyncInfo.LastValue
		return false
	} else if clientSyncInfo.RpcID == args.RpcID {
		reply.RpcID = clientSyncInfo.RpcID
		clientSyncInfo.RpcID++
		return true
	} else {
		log.Printf("Client %d RPC ID: %d, Server Client RPC ID: %d\n", args.ClientID, args.RpcID, clientSyncInfo.RpcID)
		panic("Client RPC greateer than server")
	}
}

func (kv *KVServer) Get(args *GetArgs, reply *GetReply) {
	value, ok := kv.table.Load(args.Key)
	if ok {
		value.(*Value).RLock()
		reply.Value = value.(*Value).value
		value.(*Value).RUnlock()
	}
}

func (kv *KVServer) Put(args *PutAppendArgs, reply *PutAppendReply) {
	if !kv.syncRpcID(args, reply) { return }

	val, ok := kv.table.LoadOrStore(args.Key, &Value { value: args.Value })
	if ok {
		val.(*Value).Lock()
		defer val.(*Value).Unlock()
		val.(*Value).value = args.Value
	}
}

func (kv *KVServer) Append(args *PutAppendArgs, reply *PutAppendReply) {
	if !kv.syncRpcID(args, reply) { return }

	val, ok := kv.table.LoadOrStore(args.Key, &Value { value: args.Value })
	if ok {
		val.(*Value).Lock()
		defer val.(*Value).Unlock()
		reply.Value = val.(*Value).value
		// also update client Append LastValue
		clientInfo, _ := kv.clientsSync.Load(args.ClientID)
		clientInfo.(*SyncInfo).LastValue = val.(*Value).value

		val.(*Value).value += args.Value
	}
}

func StartKVServer() *KVServer {
	kv := new(KVServer)

	// You may need initialization code here.

	return kv
}
