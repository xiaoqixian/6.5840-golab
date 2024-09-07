// Date:   Wed May 15 13:38:51 2024
// Mail:   lunar_ubuntu@qq.com
// Author: https://github.com/xiaoqixian

package kvraft

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"sync/atomic"
	"time"

	"6.5840/labrpc"
	"6.5840/raft"
)

var ClerkID atomic.Uint32

type Clerk struct {
	servers []*labrpc.ClientEnd
	// You will have to modify this struct.
	id int
	rpcID int
}

func nrand() int64 {
	max := big.NewInt(int64(1) << 62)
	bigx, _ := rand.Int(rand.Reader, max)
	x := bigx.Int64()
	return x
}

func MakeClerk(servers []*labrpc.ClientEnd) *Clerk {
	ck := new(Clerk)
	ck.servers = servers
	ck.id = int(ClerkID.Add(1))
	// You'll have to add code here.
	return ck
}

type ServerGetStateReply struct {
	id int
	reply *GetStateReply
}

// find a possible leader.
func (ck *Clerk) findLeader() *labrpc.ClientEnd {
	ldIdx, ldTerm := -1, 0
	for ldIdx < 0 {
		replies := make(chan ServerGetStateReply, len(ck.servers))
		for i, srv := range ck.servers {
			go func(srv *labrpc.ClientEnd, id int) {
				reply := &GetStateReply{}
				srv.Call("KVServer.GetState", nil, reply)
				replies <- ServerGetStateReply {
					id: id,
					reply: reply,
				}
			}(srv, i)
		}

		timer := time.NewTimer(FIND_LEADER_WATING)
		loop: for {
			select {
			case <- timer.C:
				break loop

			case ireply := <- replies:
				if ireply.reply.IsLeader && ireply.reply.Term > ldTerm {
					ldIdx = ireply.id
					ldTerm = ireply.reply.Term
				}
			}
		}
	}
	return ck.servers[ldIdx]
}

// return a RpcReply on success
func (ck *Clerk) call(args *RpcArgs) *RpcReply {
	reply := &RpcReply {}

	for {
		ldSrv := ck.findLeader()
		for ok := ldSrv.Call("KVServer.Get", args, reply); 
			!ok;
			ok = ldSrv.Call("KVServer.Get", args, reply) {
			time.Sleep(raft.RPC_FAIL_WAITING)
		}

		switch reply.OpRes {
		case OP_DEFAULT:
			ck.fatal("Unprocessed OpReply")

		case OP_SUCCESS:
			return reply

		case OP_FAIL:

		case OP_UNSYNC:
			ck.rpcID = reply.RpcID + 1
			args.RpcID = ck.rpcID
		}
	}
}

// fetch the current value for a key.
// returns "" if the key does not exist.
// keeps trying forever in the face of all other errors.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer."+op, &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
func (ck *Clerk) Get(key string) string {
	ck.rpcID++
	args := &RpcArgs {
		ClientID: ck.id,
		RpcID: ck.rpcID,
		OpArgs: &GetArgs { key },
	}
	reply := ck.call(args)
	return reply.OpReply.(string)
}

// shared by Put and Append.
//
// you can send an RPC with code like this:
// ok := ck.servers[i].Call("KVServer.PutAppend", &args, &reply)
//
// the types of args and reply (including whether they are pointers)
// must match the declared types of the RPC handler function's
// arguments. and reply must be passed as a pointer.
func (ck *Clerk) PutAppend(key string, value string, op string) {
	ck.rpcID++
	args := &RpcArgs {
		ClientID: ck.id,
		RpcID: ck.rpcID,
		OpArgs: &PutAppendArgs {
			Key: key,
			Value: value,
		},
	}

	ck.call(args)
}

func (ck *Clerk) Put(key string, value string) {
	ck.PutAppend(key, value, "Put")
}
func (ck *Clerk) Append(key string, value string) {
	ck.PutAppend(key, value, "Append")
}

func (ck *Clerk) log(format string, args ...interface{}) {
	log.Printf("[Clerk %d] %s", ck.id, fmt.Sprintf(format, args...))
}
func (ck *Clerk) fatal(format string, args ...interface{}) {
	log.Fatalf("[Clerk %d] %s", ck.id, fmt.Sprintf(format, args...))
}
