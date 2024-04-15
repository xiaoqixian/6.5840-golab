package kvsrv

// Put or Append
type PutAppendArgs struct {
	Key   string
	Value string
	ClientID uint32
	RpcID uint32
	// You'll have to add definitions here.
	// Field names must start with capital letters,
	// otherwise RPC will break.
}

type PutAppendReply struct {
	RpcID uint32
	Value string
}

type GetArgs struct {
	Key string
	// You'll have to add definitions here.
}

type GetReply struct {
	Value string
}

