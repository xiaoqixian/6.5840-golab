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

##### Apply 操作的幂等性

​	本 lab 设计的 Apply 操作具有幂等性, 即同一个index 的 log 可以被多次提交, 只需要与其它节点的同 index log 保持相同即可. 

​	幂等性保证了节点不用担心 crash 之后多次 apply 的问题, 假如节点刚好在提交了一个log之后、更新 LAI 之前 crash. 则在重启之后由于 LAI 没有来得及持久化, 所以之前已经提交的 log 将再次提交, 而 lab 保证了多次提交不会影响测试结果. 

##### 什么样的数据需要持久化?

​	Raft 论文中提出需要持久化的数据包括 term、voteFor 和 logs,  但我认为除了 logs 需要持久化之外, 其它的状态信息均不需要持久化. 

1. 为什么 term 不需要持久化?

   如果选择不持久化, 则节点从 follower 角色、 term 为 0 开始工作. 此时, 若集群存在 leader, 在接受 `AppendEntries` 信息时, 节点的 term 就可以迅速与 leader 同步. 

2. 为什么 voteFor 不需要持久化?	


##### 快速同步

​	在 `TestFigure8Unreliable` 中, 会测试网络环境非常不稳定以及 leader 非常容易宕机的情况下的同步情况下的一致性情况. 在测试中会进行上千次的迭代, 每次迭代会开始一个命令, 由于每次迭代中 leader 都有概率宕机, 导致大部分命令来不及分发集群就丧失了 leader, 必须再次选举. 因此大部分命令都是无效的.

​	最后测试会恢复所有节点的网络, 然后开始一条命令, 测试集群的一致性. 这时由于大部分命令都是无效命令, 只有最开始几条命令是同步的, 因此 leader 节点与 followers 达到同步的过程非常长, 超出了规定的测试时间, 必须设计一个可以在两个节点大部分 log 不一致也能快速达到同步的算法.

​	基于 log 索引的有序性, 可以使用二分搜索找到刚好未达到一致的log. follower 基于匹配情况返回如下几种状态:

- `ENTRY_EXISTS`: prev log 和 curr log 都存在, 且匹配. leader 在收到后应该在 prev log 之后的 logs 里面寻求匹配;
- `ENTRY_MATCH`: prev log 匹配, 但是 curr log 不匹配, 说明找到了匹配点, 则 follower 将原 prev log 之后的所有 logs 全部删除, 然后替换为 curr log; leader 收到之后将 curr log 作为 prev log, 将 curr log 之后的所有 logs 作为 curr log 发送给 follower, 从而实现了双方的快速同步.
- `ENTRY_FAILURE`: prev log 无法匹配, leader 收到后需要在 prev log 之前的 logs 里面寻求匹配.

