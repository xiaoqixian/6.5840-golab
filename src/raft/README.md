##### 测试环境

​	使用 lab 提供的 `labrpc` 包进行 RPC 调用, 当开启 `reliable=false` 的设置, 将有1/10 的概率丢包, 此时 `Call` 接口直接返回 `false`.

​	当开启 `longreordering=true`, 将有 2/3 的概率延迟 200~2200 ms 后才返回结果, `Call` 调用在这段时间内会持续阻塞.

​	在 `TestFigure8Unreliable3C` 中会开启 `longreordering=true` , 此时 leader 将特别容易与某个节点丧失联系, 接着导致该节点重新发起选举, 导致系统的可用性急剧下降. 

​	解决办法是在另外的 goroutine 中调用 RPC, 使用一个 channel 返回结果, 然后使用一个 ticker 当 RPC 调用时间过长时重新发起一次调用, 直到返回.

```go
func rpcMultiTry(f func() bool) (ok bool) {
	ticker := time.NewTicker(RPC_FAIL_WAITING)
	ch := make(chan bool, 1)

	loop: for {
		go func() {
			ch <- f()
		}()

		select {
		case ok = <- ch:
			break loop
		case <- ticker.C:
		}
	}
	return
}
```

##### 基础架构

​	使用基于 event + channel 的架构, 节点在被创建之后会创建一条 goroutine 用于处理所有事件, 这个过程是串行的, 可以减少很多竞争. 

###### 为什么不使用传统的多线程+mutex的方式?

​	因为节点的行为非常难以掌控, RPC 调用和其它事件可能随时到达, 这些事件可能会导致节点的身份发生转换, 而在转换之时节点可能同时正在基于当前身份处理其它事件. 如果使用细粒度的锁将会带来非常大的心智负担, 使用粗粒度的锁则和串行处理的效率相差无几. 

​	借助 golang 非常方便的 channel, 可以在节点内部定义一个 event channel, 然后将所有的调用转换成事件发送到该 channel 内, 该事件内含一个 channel 用于通知发送方已经处理完成. 事件是否被处理则完全取决于节点当前角色. 

​	譬如, 对于 `AppendEntries` RPC 调用的函数

```go
func (rf *Raft) AppendEntries(args *AppendEntriesArgs, reply *AppendEntriesReply) {
	if !rf.dead.Load() {
		ch := make(chan bool, 1)
		rf.chLock.RLock()
		rf.evCh <- &AppendEntriesEvent {
			args: args,
			reply: reply,
			ch: ch,
		}
		rf.chLock.RUnlock()
		reply.Responsed = <- ch
	}
}
```

当 `AppendEntries` 被调用时, 则向 `evCh` 中发送一个 `AppendEntriesEvent`. 

​	基于 event channel , 设计了「事件处理主线程」+ 「事件发送 goroutine」的架构. 节点的状态只能在主线程中进行修改, 而其它 goroutine 只能对状态进行读取(需要跨协程分享的变量使用原子变量进行表示). 从而保证多线程安全.

##### 角色转换过程

​	基于 Raft 算法, 每个节点应当有三种角色, 当出现特定的事件时, 节点需要进行角色转换. 角色转换的过程统一由 `transRole` 函数完成

```go
func (rf *Raft) transRole(f func(Role) Role) {
	if rf.role.stop() {
		rf.role = f(rf.role)
		rf.evCh <- &TransEvent{}
	}
}
```

其中参数 `f` 表示进行角色转换的函数. 

​	在进行角色转换时, 节点会尝试将角色停止, 然后通过函数生成一个新的角色, 最后向 event channel 中发送一个 `TransEvent` 表示已经进行了一次角色转换. 

​	可以看到在这之中并没有任何获取锁的过程, 因为所有的事件都是向 event channel 发送事件来处理的, 而不是直接调用函数处理的, 也就不存在竞争. 

​	`transRole` 在主线程的任何地方直接调用都是线程安全的, 而其它 goroutine 需要节点进行角色转换时, 则向 event channel 发送一个对应的事件来完成. 例如, 当 follower 的心跳信息过期时, 会发送一个 `HeartBeatTimeoutEvent` 的事件, 主线程在处理到该事件时就调用 `transRole` 函数进行角色转换. 

##### 如何保证事件在角色之间的隔离?

​	由于每个角色都不可避免地创建一些 goroutine, 这些 goroutine 无法与主线程保持串行的执行顺序. 虽然我们让所有的 goroutine 都只向主线程发送事件的方式来修改角色状态, 但是这样做也有弊端, 即事件的发送可能跨角色. 

​	为了保证这个约定始终成立, 需要做出下面的设计: 当 goroutine 要向 event channel 加入事件时, 要么在 `TransEvent` 之前放入, 要么不要放入. 要做到这点, 还是需要借助锁和原子变量.

1. 为 event channel 添加一个读写锁, 主线程(即 `processor`  函数) 取事件时不需要访问锁; 而 goroutine 放入事件时需要尝试获取读锁, 获取失败时应该立刻放弃放入事件. 

   若获取到了读锁, 则应该检查 `active` 是否为 `true`, 若为 `false`, 则说明角色已经停止, 不应该放入事件. 

2. 当角色转换时, 需要阻塞式获取写锁, 保证没有协程正在放入事件, 然后放入 `TransEvent`. 同时将 `active` 原子变量置 `false`. 这样即便 goroutine 等到了下一个角色开启时获取到了读锁, 也因为 `active == false` 为拒绝放入事件, 从而保证不会有事件污染.

3. 主线程每次事件时, 会先检查当前事件是否为 `TransEvent`; 若是, 则将当前角色 `activate`; 

   若否, 则检查角色是否已经停止, 若停止, 则直接丢弃事件, 而事件的发送方本身已经做好了事件被丢弃的准备.

##### 选举过程

```go
type RequestVoteArgs struct {
	// Your data here (3A, 3B).
	Term int
	CandidateID int
	LastLogInfo LogInfo
}
type RequestVoteReply struct {
	Term int // for candidate to update itself
	VoterID int
	VoteStatus VoteStatus
	Responsed bool
}
```

​	candidate 会在自己的选票请求中放入自己的 term 和 last log 的 index 和 term, 收到请求的节点基于自己 last log 的情况, 给出 `VoteStatus` 

1. `VOTE_GRANTED`: candidate term 大于自己, 且 last log 至少和自己的一样新;
2. `VOTE_OTHER`: 当前节点不是 leader, 且 candidate term 小于等于自己 或 last log 没有自己的新;
3. `VOTE_DENIAL`: 当前节点是 leader, 且 candidate 的 term 小于等于自己的. 

candidate 只有在收到 `VOTE_GRANTED` 时才能认为自己收到了一张选票, 当收到 `VOTE_OTHER` 时则认为对方拒绝了自己的选票, 继续选举; 当收到 `VOTE_DENIAL` 时, 则说明来自 leader, 其认为 candidate 不足以赢得选票, 应该直接放弃选举. 第三种情况是一种优化方式, 只有 `VOTE_GRANTED` 和 `VOTE_OTHER` 的情况下该 candidate 依然无法获得选举, 但是 `VOTE_DENIAL` 可以让该 candidate 更快放弃选举, 减少网络中 RPC 的传递数量. 

##### 计票方式

​	candidate 基于 bitmap 的方式进行计数, 而不是单纯的计数器进行选票计数. 这是为了防止 RPC 重复到达导致选票误计的情况. 

​	每当有一张选票到达时, 就检查是否已经收到大部分节点的选票, 票数达标之后就主动转换为 leader.

##### Follower 添加 log 过程

```go
type AppendEntriesArgs struct {
	Id int
	Term int
	PrevLogInfo LogInfo
	LeaderCommit int
	Entry *LogEntry
}
```

1. 检查 `term` 是否小于自己, 若小于说明是旧 leader, 则拒绝请求;
2. 检查 prev log 是否匹配, 若不匹配, 则回复 “匹配失败”;
3. 检查 current log 是否存在和匹配; 若不存在, 则添加后回复成功; 若匹配, 则直接回复成功; 若不匹配, 则将 current log 及之后的 logs 均删除后添加, 然后回复成功.

##### Follower 更新 LCI 的过程

​	Leader 在收到大多数节点关于某个 log index 的肯定回复之后, 需要将自己的 LCI 更新. 并需要在每一次 AppendEntries RPC 中包含这个 LCI, 以便其它节点更新自己的 LCI 并及时 apply. 

​	Follower 在拿到 `LeaderCommit` 之后并不能直接将自己的 LCI 更新到与之相同, 因为 Follower 并不能确定自己的 logs 与 Leader 的 logs 在 LCI 及之前的索引是完全匹配的, 只有在完全匹配的情况下才能更新自己的 LCI. 

​	基于此点, 当 follower 在 append entries 的时候需要进行 prev log 匹配. 若匹配成功, 则可以认为在 prev index 及之前的 logs 是匹配的, 此时 follower 的 LCI 可以更新到 `min(LeaderCommit, PrevLogInfo.Index)`. 

##### 是否存在 log index follower 先于 leader commit 的情况?

​	存在这样一种情况, 当 leader 收到了足够数量的 log 复制回复之后(假设集群的所有log已经达到完全一致), 将自己的 LCI 进行更新, 并在随后的 `AppendEntries` 消息中告诉其它节点, 但是 leader 只来得及告诉部分节点就宕机了, 导致出现集群中 LCI 不一致的情况. 

​	但是由于所有节点的 logs 是一致的, 所以所有节点均有机会赢得选举, 若 LCI 没有及时更新的节点赢得选举, 则出现了 leader 的 LCI 小于 follower 的情况. 

​	这种情况依然是安全的, 当 follower 检测到这种情况时, 直接确认已复制即可.

#### Persistence

###### 测试逻辑

​	lab 提供 `Persister` 的实现, 在每一次创建节点的时候传入一个实例的复制, 其中 `raftstate` 字段存储有节点实例持久化的状态, 为 `[]byte` 类型.  所以本质上 `Persister` 就是 lab 在让节点崩溃时为我们保存一段字节数组, 以便在节点重启时恢复崩溃前的状态. 

##### 什么样的数据需要持久化?

​	需要持久化的数据主要为 Term 和 logs 相关的数据

```GO
type PersistentLogs struct {
	Entries []LogEntry
	Lci int32
	Lli int32
	Lai int32
	NoopCount int
	Offset int
}
type PersistentState struct {
	Term int
	Logs PersistentLogs
}
```

Raft 算法的论文提到还有 `voteFor` 的信息需要持久化, 但是我没有选择这样做. 因为

1. 节点在投票后立刻宕机的可能性比较低
2. 即便节点在投票后宕机, 也比较难影响其所投票的对象是否当选, 除非节点的投票回复信息在网络中丢失, 而节点刚好投票后宕机. 这个过程会造成节点已经投票但是candidate没有收到的情况.
3. 即便如此, 并不会影响

##### Fast Match

​	在 `TestFigure8Unreliable` 中, 会测试网络环境非常不稳定以及 leader 非常容易宕机的情况下的同步情况下的一致性情况. 在测试中会进行上千次的迭代, 每次迭代会开始一个命令, 由于每次迭代中 leader 都有概率宕机, 导致大部分命令来不及分发集群就丧失了 leader, 必须再次选举. 因此大部分命令都是无效的.

​	最后测试会恢复所有节点的网络, 然后开始一条命令, 测试集群的一致性. 这时由于大部分命令都是无效命令, 只有最开始几条命令是同步的, 因此 leader 节点与 followers 达到同步的过程非常长, 超出了规定的测试时间, 必须设计一个可以在两个节点大部分 log 不一致也能快速达到同步的算法.

​	基于 log 索引的有序性, 可以使用二分搜索找到刚好未达到一致的log. follower 基于匹配情况返回如下几种状态:

- `ENTRY_EXISTS`: prev log 和 curr log 都存在, 且匹配. leader 在收到后应该在 prev log 之后的 logs 里面寻求匹配;
- `ENTRY_MATCH`: prev log 匹配, 但是 curr log 不匹配, 说明找到了匹配点, 则 follower 将原 prev log 之后的所有 logs 全部删除, 然后替换为 curr log; leader 收到之后将 curr log 作为 prev log, 将 curr log 之后的所有 logs 作为 curr log 发送给 follower, 从而实现了双方的快速同步.
- `ENTRY_FAILURE`: prev log 无法匹配, leader 收到后需要在 prev log 之前的 logs 里面寻求匹配.

总结一下 leader 新上任后对某个节点开始 replication 的流程, 需要注意的是 follower 不会记录当前 leader 的id, 只会基于 term 来决定要不要接受 `AppendEntries` RPC.

​	首先修改 `AppendEntriesArgs` 允许一次添加多个 logs

```go
type AppendEntriesArgs struct {
	Id int
	Term int

	PrevLogInfo LogInfo
	LeaderCommit int
	Entries []LogEntry
}
```

对于每个 peer, leader 会发起一条 replicator goroutine 用于复制 logs. 

1. replicator goroutine 启动后, 开始二分搜索同步过程. 
2. 定义 `prevLogIndex := -1`, 表示初始状态下 peer 没有与 leader 同步的 log. 接着在 `[0, LLI]` 的区间寻找更新 `prevLogIndex`.
3. 最后令 replicator 的 `nextIndex = prevIndex + 1`.

#### Log Compaction

##### 测试逻辑

​	3D 测试要求实现一个 `Snapshot(index int, snapshot []byte)` 方法, 其中第一个参数表示 snapshot 中打包的 logs 中最大的 index.

​	在测试中对于每个节点对应一个 Applier, 负责接收 `applyCh` 中到达的 `ApplyMsg`.

```go
type ApplyMsg struct {
	CommandValid bool
	Command      interface{}
	CommandIndex int

	// For 3D:
	SnapshotValid bool
	Snapshot      []byte
	SnapshotTerm  int
	SnapshotIndex int
}
```

 每当 Applied Index 增长一轮, 则会触发调用 `Snapshot` 方法. 测试函数会将当前 applied index 及之前所有的 log 数组编码得到 `[]byte`, 表示一个 snapshot. 然后调用 `Snapshot` 完成了一次快照. 

​	对于落后太多的 peer 节点, leader 节点会首先发送一个 `InstallSnapshot` RPC, 该 peer 节点接收到后则基于 RPC 中的 `lastIncludeIndex`, 将该 `lastIncludeIndex` 及之前的所有 logs 全部删除, 随后向 `applyCh` 发送一个 `ApplyMsg`, 并置 `SnapshotValid = true` 表示这是一个 Snapshot. 

​	在测试中, tester 会对比 `ApplyMsg` 中的 `SnapshotIndex` 与 `Snapshot` 解码后的 `LastIncludeIndex` 是否一致, 只有一致时才能说明集群依然保持了一致性.	

##### 对 Logs 模块的影响

​	节点的 logs 模块在取快照之后, 需要将之前的 logs 全部删除. 在原本的设计中, log index 与 logs 数组中的 index 是一一对应的, 若删除部分 logs, 将导致这种对应关系丢失. 因此需要在 logs 模块中添加一个 `offset` 字段, `logIndex - offset` 即是该 log 在数组中实际的 index.

​	在每一次 snapshot 中, 实际需要删除的 logs 则是 `entries[0:idx-offset+1]` 范围内的logs.

##### 并发

​	在加入了 snapshot 以后, `logs.entries` 需要使用读写锁进行保护. 

- 需要获取写锁的情况

  当 tester 调用 `Snapshot` 函数时, Leader 需要获取写锁, 因为需要删除 Entries 部分内容, 以及将节点中存储的 `snapshot` 进行更换.

  当 follower 接收到 leader 的 snapshot 时, 需要获取写锁, 这与主动进行快照一致.

- 需要获取读锁的情况

  replicator goroutine 根据所复制的节点情况可能需要读取 Entries 和 snapshot. 不管是哪种情况, 都需要获取读锁. 

  两种情况由 `entriesAfter` 函数统一控制, 其根据所寻找的 log index 决定返回 snapshot 还是 `[]LogEntry`. 

##### `InstallSnapshot` RPC

​	在 Raft 论文中, Snapshot 分发是通过一个单独的 RPC 调用完成的. 但是为了复用大部分代码, 可以通过修改 `AppendEntriesArgs` 来分发 Snapshot. replicator 具体逻辑为

1. 开始后基于二分搜索寻找 `nextIndex`;
2. 使用 `nextIndex` 调用 `entriesAfter` 方法; 若返回 `*Snapshot != nil`, 则说明该 peer 的匹配点已经位于 Snapshot 之中, 则发送一个 Snapshot 的 `AppendEntries` RPC, 令 `nextIndex = snapshot.lastIncludeIndex+1`;
3. 否则分发 `[]LogEntry`. 
4. 当 follower 接收到 snapshot 时, 将 snapshot 存储, 将已有的 entries 全部删除, 然后将 `LLI, LCI, LAI` 统一更新为 `Snapshot.LastIncludeIndex`. 并回复 `ENTRY_MATCH`. 

##### Noop Count

​	当 follower 接受 leader 的 snapshot 时, 需要将自己的 entries 全部删除, 此时需要重置 follower 的 `noopCount`. 

​	因此需要在 `takeSnapshot` 时, 遍历所有的 entry 找到其中的 `NoopEntry`, 然后记录在下面的结构体中

```go
type Snapshot struct {
	Snapshot []byte
	LastIncludeIndex int
	LastIncludeTerm int
	NoopCount int
}
```

#####  Binary Search

​	在 replicator goroutine 中, 需要通过二分搜索找到 peer 刚好匹配的 log. 在加入 snapshot 机制后, 搜索的范围从 `[0, LLI]` 变为 `[LII, LLI]`, 其中 `LII` 表示 `LastIncludeIndex`. 

​	在搜索过程中由于需要获取 `Entries` 任意 entry 的 term, 这要求在搜索过程中 entries 是不变的, 因此需要在 `matchIndex` 中获取读锁. 

​	正常来说这种做法并没有问题, 但是在测试中, 当对应的 peer 被断连时, 将导致 `Call` 函数持续阻塞, 从而导致读锁持续被占用, 则 tester 无法完成 snapshot. 而糟糕的是, 由于节点采用串行的事件处理机制, 当 `SnapshotEvent` 的处理被阻塞时, 将导致后续的事件处理也被阻塞. 因此需要设计一种 只在创建 `AppendEntriesArgs` 参数时才需要占用读锁的二分搜索方法, 这种方法要求在搜索过程中出现部分 entries 被删除也可以成功找到匹配点.

1. 定义 `tryIndexLogTerm` 方法, 当尝试获取小于 LII 的 entry term 时, 并不直接报错, 而是返回 false. 

2. 定义双层循环, 在第一层循环中确定搜索边界 `[LII, LLI]`; 

   第二层循环中取中间位置调用 `tryIndexLogTerm`, 若获取失败, 则说明已经发生了一次 snapshot; 则回到第一层循环, 获取左右边界值后重新开始.

##### 问题

1. 在 `TestSnapshotInstallCrash3D` 测试中, 在 `Logs.updateCommit` 函数中出现了需要 Apply 的 entries 索引小于 0 的情况. 

   确定 Apply 范围的代码行 为

   ```go
   st, ed := logs.LAI()-logs.offset+1, newCommit-logs.offset+1
   ```

   问题出现在起始索引 `st<0` 上, `logs.lai` 是一个原子变量, 而 entries 的 apply 过程是另一个协程通过 channel 来完成的. 当每次更新 LCI 以及 Install Snapshot 时, 就向该 channel 内发送一个特定的对象表示相应的 entries 可以被 apply, apply 之后再由该协程更新 LAI. 

   提前更新 LAI 不是一个好的选择, 因为 tester 允许我们重复 apply, 但是不允许漏 apply. 如果在 apply 的过程中出现 crash, 将导致 LAI 已经更新但是 tester 没有接收到 applied entries 的情况. 

   因此我考虑取消单独的 applier goroutine, 直接在接收到 entries 和 snapshot 的时候就 apply, 然后更新 LCI.

2. 上面的操作带来了新的问题, tester 进行 snapshot 的时刻非常可能发生在一次连续的 apply 操作的中间. 

   在 `Snapshot` 函数中会创建一个 `SnapshotEvent`, 然后一直等待处理. 但是由于主线程在完成 apply 操作, 无法立刻处理该事件; 而该事件没有处理返回, tester 就无法继续接收 `ApplyMsg`. 从而造成了死锁.

3. 现在的一个补救办法就是在事件机制上开个后门, `SnapshotEvent` 放入之后就直接返回, 并不进行等待. 