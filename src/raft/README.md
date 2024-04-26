### Lab3 Raft

#### Architectures

​	在架构层面, 贴合 golang 的设计哲学, 尽可能地使用 channel 而不是锁. 对所有的 RPC 调用使用 channel 进行序列化处理, 保证每个节点在同一时刻只处理一个请求.

##### RPC Processor

​	一个节点层面(我将代码分为节点层面和角色层面) 运行的循环, 相当于 request channel 的消费端, 负责不断从 channel 中取出请求并基于请求类型分发给角色层面的处理函数, 从接口层面保证了所有的角色都具有相同的处理函数. 

​	这个循环是单线程运行的, 尽可能减少并发度, 因为在代码上并没有很多计算复杂的逻辑, 所以使用单线程处理完全可以 hold 住. 

#### A. Leader Election

​	节点投票机制: 

- 若候选者的任期小于等于自己, 则拒绝投票. 并回送自己当前leader的term和 leader id.

  为什么要求候选者的任期必须大于自己? 

  首先小于自己为什么拒绝的情况不需要讨论, 在等于自己的情况下, 则说明该候选者在开始选举之前的 term 刚好小于自己的 term. 这并不能说明该候选者没有资格参加选举, 因为可能存在这种情况: 

  - 假设候选者的 term 为 T1, 本节点的 term 也是 T1. 而本节点的 T1 term 来自于本节点最近发送 AppendEntries RPC 中 term 最大的 leader. 
  - 该 leader 可能在刚开始发送心跳信息到少数节点之后就宕机了, 使得只有少数几个节点的 term 更新到了 T1. 大多数节点的 term 还是 T1-1 或者更小, 而且这部分节点由于没有收到心跳信息所以更先心跳信息 timeout, 并发起选举. 由于 term 为 T1 的节点只有少数节点, 所以即便它们拒绝给出选票, 仍然可能赢得选举, 新选举的 leader 的 term 也是 T1, 少部分term为 T1 的节点也会承认该 leader.

- 若候选者的任期大于自己, 则给予选票, 并将自己的任期更新到该候选者的任期, 并重置自己的 timer.

  之所以候选者还没有获选时, 跟随者就直接更新自己的 term. 是因为我们只需要保证 term 是递增的, 而并不一定是要连续的.

  若 follower 在投票时不更新自己的 term, 则可能出现这样的情况: candidate  已经获取了大多数节点的选票, 则更新自己的状态为 leader, term 为 t, 其它节点此时的 term 依旧为 t-1 或更小.  若 leader 还未来得及给其它节点发送心跳信息宣示自己的存在就宕机了, 则其它节点开始新一轮选举, 选举的 leader 的 term 也是 t, 而原 leader 宕机重启之后依然认为自己是 leader, 则将导致集群中出现两个 term 相同的 leader, 违背了一致性原则. 

  因此, follower 在给出选票的同时必须更新自己的 term, 换句话说, 当 follower 可以认为自己给出选票的对象就是自己的 leader.

候选者期望得到的选票回复为以下几种:

1. `VOTE_GRANTED`: 给票
2. `VOTE_DENIAL`: 拒绝给出选票, 因为候选者的 last commit index 小于自身;
3. `VOTE_OTHER`: 已经投票给其它节点, 候选者需要检查对应回复信息中的 term 是否大于自身, 如果是, 则应该退出选举. 

##### 	群雄割据

- 同一时刻, 集群可能存在多个候选者“割据”的状态, 并且它们的 term id 可能相同, 在这种情况下可能无法成功选举出一个leader. 

  基于这种情况, 候选者的选举实际是一个循环, 当循环结束而候选者的状态仍然是 `POLLING` 时, 说明此次循环没有获胜者, 随机沉睡一段时间后继续选举, 重新选举需要清空所有选票, 并将自己的候选任期加一.

  直到以下三种情况之一发生:

  1. 在选举中被击败, 收到 term 大于自己的 候选者发送的 RequestVote
  2. 赢得选举
  3. 选举遭到打断, 例如收到不小于自己候选任期的服务器的 AppendEntries RPC, 则说明集群内已经选举产生一个 leader, 只是自己还没有收到消息. 则立刻停止选举, 回退为一个 跟随者.

##### 特殊情况:

1. 倘若一个已达到稳定的集群中出现一个节点网络中断, 假设节点id为1, 其它节点继续稳定运行, 假设集群此时大部分节点的 term 为 1, 中断节点的 term 也为 1, last commit index 均为1. 

   随后该节点网络恢复, 由于网络中断过久, 此时节点的心跳信息已经 timeout, 于是节点发起选举, 将自己的 term 加1 得到 2, last commit index 依旧为1.

   而此时集群其它节点还在平稳运行, 并没有发生换届, 因此 term 依旧为1, 但是 last commit index 已经更新到 5.

   在这种情况下,  节点1 显然不能赢得选举, 因为其并没有足够新的 last commit index, 其它节点不会给出选票, 因此该节点会一直陷入到 发起选举-> 无法获得足够的选票 -> 随机等待一段时间 -> 发起选举的循环之中, 并且由于每次选举的迭代中其会自增自己的 term, 导致即便集群出现新的 leader, 该 leader 的 term 小于该节点的可能性依然很大. 

   为了防止网络中断节点陷入这种循环之中, 添加以下的机制: 当 candidate 收到更小 term 的 AppendEntries RPC 时, 其拒绝该 RPC, 并回复自己的 term. leader 在收到以后即认为自己已经“过时”(尽管实际上并没有), 转换为 follower. 此时集群进入“群龙无首” 的状态, 随后出现携带 last commit index 的节点心跳 timeout 之后发起选举并赢得选举, 进入下一阶段. 

   

#### 并发与锁

##### channel 保护锁

​	当节点进行角色转换时, 需要将 channel 清空, 做法就是将 channel 重新赋值给一个新的 channel. 但是由于 RPC 调用随时会到达, 所以需要用一个读写锁进行保护. 

​	当 RPC 调用发生时, 必须要抢占读锁才可以向 channel 发送请求; 在进行角色转换时, 则需要等待 写锁. 

​	那 RPC processor 线程是否需要等待读锁呢? 答案是不需要, 因为我们可以保证 RPC process 每次从 channel 获取请求时, 总是处于角色的“运行” 状态而不是 “转换中” 状态. 

##### 安全的身份转换

​	在 Raft 中存在多种随机时限之后的身份转换, 身份转换要求能够角色运行的状态可以被安全地打断, 然后跳转到另一个状态中. 

​	为了保证角色协程的运行可以随时被打断, 需要使用读写锁进行保护. 然后在角色的操作开始前要尝试获取读锁, 获取失败时只可能表示角色已经发生转换, 应该立刻退出.

​	以 follower heartbeat timeout 之后跳转到 candidate 为例. 为 follower 定义一个读写锁, 对于每一次外部调用, 如 `requestVote` 或 `appendEntries`, 则试图占用读锁, 如果无法占用, 则直接返回; 

##### Candidate status atomic value

​	候选者总共有五种状态:

```go
type CandidateStatus uint8
const (
	POLLING CandidateStatus = iota
	ELECTED
	DEFEATED
	POLL_TIMEOUT
	CANDIDATE_KILLED
)
```

1. 初始状态下为 `POLLING`, 表示正在进行选举;
2. 选举的状态可以随时被打破:
   1. 选举 timeout, 转换为 `POLL_TIMEOUT`
   2. 获得足够的选票, 转换为 `ELECTED`
   3. 收到更大 term 的 `RequestVote` 或者至少一样大 term 的 `AppendEntries`.
3. 上面的状态转换均需要获取锁, 然后判断当前状态是否为 `POLLING` 再进行.

`ELETED`, `DEFEATED`, `POLL_TIMEOUT` 等状态的进入需要以当前状态为 `POLLING` 为前提. 但在一些临界情况下依然无法保证原子性, 我们需要设置一些额外的状态来辅助完成. 例如, 当对选举信息进行修改时(term自增、选票清零等), 我们不希望其它 RPC 调用在此期间访问这些信息. 在这种情况下, 我们可以将 CandidateStatus 设置为 `POLL_UPDATING`, 当 RPC 调用需要访问这些信息时, 需要进行自旋等待, 直到修改完成.

#### 数据项分发

​	API 接口要求返回三个值: 第一个表示命令如果被正确提交将会出现的索引, 第二个表示命令所在的term, 第三个表示当前节点是否认为自己是 leader.

​	Leader 首先需要维护一个数组用于存放所有的 Entry, 当一个 Leader 新上任时, 该数组应该只包含所有已提交的 Entry. 当客户端提交命令时, 直接将命令封装为 Entry 后放到数组尾部. 

​	同时 Leader 需要维护另一个数组, 表示所有的节点(包括自己)已提交的 Entry 的索引的下一位, 即 leader 的 commit 索引所指向的 entry 就是接下来需要提交的 entry, 不应存在节点的索引比 leader 的更大. leader 新上任时, 默认所有的节点的 entry 与自己的相同, 而后通过发布一个 `NoopEntry` 到各节点摸清各节点已提交的 entry 索引信息, 从而保持同步.

​	Leader 上任时, 会为每个 peer (不包括自己) 开一个协程, 用于分发 entry, 这些协程会定期检查 entry 数组是否有可分发的 entry, 这个过程需要由 leader 封装, leader 不会透露超过自己的 commit index 之后的 entry, 防止出现提交不一致的情况. 随后该协程通过 RPC 调用分发到各 peer 节点, 分发成功后通过一个 channel 告知 leader 的某个协程. leader 在确定自己的 commit 索引处的 entry 已经大部分提交了以后, 自动加一. 

##### Follower 添加 entry 流程

1. 检查 `prevLogIndex`, 若大于自己的 LCI, 则说明自己落后于 leader, 回复 `false`.
2. 若刚好等于自己的 LCI, 则说明是新的 entry, 直接添加到 LCI 之后的位置上(不管该位置是否存有entry).

#### Leader

##### init

- 删除 last commit index 之后的所有 log entries.

- 开启发送心跳信息的协程, 定期向其它 peers 发送空 `AppendEntries` RPC

- 开启向其它 peers 发送 replications 的协程, 每个 peer 对应一条协程; 该协程不断检查对应 peer 是否有可以发送的 log entry. 当 peer 对一条 entry replication 表示确认时, 发送一个 `ReplConfirm` 到 replication confirmation channel.

  ```go
  type ReplConfirm struct {
  	index int
  	peerId int
  }
  ```

- 开启回复确认的协程, 负责统计peers的回复确认, 并更新自己的 last commit index.

- 开启一个等待退休消息的协程, 有多条可能让 leader 退休的协程, 

- 发送一个 `NoopEntry` 给自己, 从而将前任的一些 entries 复制到那些未跟上的 peers.

##### request vote

​	基于 vote request term 可能有两种情况:

1. `vote term <= leader term`: 则认定为非法选举, 回复 `vote denial`. 
2. `>`: 合法选举, leader 应该自愿让权, 转换为 follower, 同时将自己的 term 更新为 `vote term`.

##### append entries

​	

##### 主从Log同步过程

​	当 leader 上任时, 默认所有 follower 的 log 索引与自己相同, 然后通过发送 AppendEntries RPC 达到同步. 

​	follower 收到 AppendEntries RPC 时, 无法确认自己是否已经与 leader 达到同步. 所以 leader 需要在 RPC 中包含自己的前一个 entry 的 term 和 index. 

​	当 follower 发现自己的 last commit index 无法与 RPC 中的信息匹配时(需要任期和索引均匹配), 则置 `Success: false`, 并置 `term` 为自己的 `term`. 

​	leader 在收到回复之后, 首先检查是否成功; 若成功, 则继续分发接下来的 entry. 若失败, 首先检查term是否大于自己的term, 若是, 则说明 leader 已经不被认可, 则触发“退休”机制, 退化为一个 follower, 重新开始流程.

##### Leader commit 过程

​	Leader 需要一个分发次数确认机制, leader 每收到一个客户端的 entry, 需要将该 entry 分发到所有 follower. 随后其会收到这些 follower 的回复, 每收到一个肯定回复, 其需要在内部记录此状态, 在收到大部分 follower 的肯定回复之后, 该 entry 可以认为已被 commit, 从而更新 leader 的 last commit index.

​	考虑到重复 RPC 的影响, 我认为不能用一个简单的计数器表示已确认复制的 follower 数量, 因此选择使用 bitmap 记录单个 entry 的回复情况. 每个 bit 对应该 entry 是否已经复制到对应的 peer 节点.

​	所有 entry 构成一个索引连续的数组, 要求保证索引必须是连续递增的. 每当有 follower 回复时, 则会检查是否可以更新对应的. 

##### 代前朝 commit

​	存在一种情况是前任 leader 在宕机前已经将 last log 分发到大多数节点上, 但是还没有来得及提交. 既然该 log 已经被分发到大多数节点, 而新 leader 获选需要获得大多数节点的选票, 因此**至少存在一个节点既存有该 log(但是还没来得及提交), 又为新 leader 投过票.**