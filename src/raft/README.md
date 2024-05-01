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

##### 如何保证事件在角色之间的隔离?

​	由于每个角色都不可避免地创建一些 goroutine, 这些 goroutine 无法与主线程保持串行的执行顺序. 虽然我们让所有的 goroutine 都只向主线程发送事件的方式来修改角色状态, 但是这样做也有弊端, 即事件的发送可能跨角色. 

​	角色转换的过程是这样: 当调用角色转换的函数 `transRole` 时, 将停止角色的运转, 同时向 event channel 发送一个 `TransEvent`, 表示发生了一次角色转换. 在停止角色的过程中, 会停止所有的 goroutine. 但是我们依然无法保证在发送 `TransEvent` 之后不会有 goroutine 发送属于该角色的 event. 这样将导致 event 溢出于属于该角色的区域, 因为我们约定 `TransEvent` 应该是每个角色 event 的起点和终点, 其它角色不应该会在 event channel 中遇到不属于该角色的 event.

​	为了保证这个约定始终成立, 需要做出下面的设计: 当 goroutine 要向 event channel 加入事件时, 要么在 `TransEvent` 之前放入, 要么不要放入. 要做到这点, 还是需要借助锁和原子变量.

1. 为 event channel 添加一个读写锁, 主线程(即 `processor`  函数) 取事件时不需要访问锁; 而 goroutine 放入事件时需要尝试获取读锁, 获取失败时应该立刻放弃放入事件. 

   若获取到了读锁, 则应该检查 `active` 是否为 `true`, 若为 `false`, 则说明角色已经停止, 不应该放入事件. 

2. 当角色转换时, 需要阻塞式获取写锁, 保证没有协程正在放入事件, 然后放入 `TransEvent`. 同时将 `active` 原子变量置 `false`. 这样即便 goroutine 等到了下一个角色开启时获取到了读锁, 也因为 `active == false` 为拒绝放入事件, 从而保证不会有事件污染.

3. 主线程处理到 `TransEvent` 之后, 将角色 `activate`, 同时将写锁释放.

##### 是否存在 log index follower 先于 leader commit 的情况?

​	理论上是存在的, 如果这个 follower 是之前的 leader 的话. 

​	存在这样一种情况, 一个 leader 在刚刚更新自己的 LCI 之后就断开连接, 随后在其它节点选举出新节点之后又恢复连接. 由于该 index 已经复制到了大部分节点, 所以新 leader 也有该 index, 但是其并不知道该 index 是否已经复制到大部分节点, 随后其开始向其 followers 确认该 index, 其中也包括 旧 leader. 在此时此刻, 该 index 在 follower (旧 leader) 上已经 commit, 但是还没有在新的 leader 还没有 commit. 

​	但是这种情况依然是安全的, 因为该 index 终究已经复制到了大部分节点, 新 leader 及其以后的所有 leader 都必须复制了该 index 才可能上任, 所以该 index 终究会被 commit.

​	当 旧 leader 遇到这种情况时, 只需要向 leader 回复自己已经复制该 index 即可.