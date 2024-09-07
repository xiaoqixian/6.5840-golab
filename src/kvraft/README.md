# Lab4: Fault-tolerant Key/Value Service

### 实验目标

​	Lab4 是 Lab2 和 Lab3 的综合. 在 Lab2 中实现了一个支持并发的单体 K/V 服务器, Lab3 实现了 Raft 算法. 而在 Lab4 中需要实现一个分布式的 K/V 服务器. 

​	Lab4 要求分别实现一个 client 和 server. 

- client 是整个集群的入口, 负责处理外部的请求.

  client 需要实现 Lab2 中的三个接口, 并且表现得如同一个单体服务器一样. 内在的细节则需要由 client 进行封装, 例如如何找到集群的 leader, 以及找到 leader 提交操作后等待操作结果, 在提交失败时重新提交等. 

  ```go
  type Clerk struct {
  	servers []*labrpc.ClientEnd
  	// You will have to modify this struct.
  }
  ```

- server 代表集群的一个节点, 内部包含 Lab3 中的一个 `*Raft` 实例. 

  server 同样需要实现相同的三个接口, 但是参数变成了 RPC 调用的参数. server 需要根据自己的角色决定是否处理请求等. 

### 实验思路

#### should we allow concurrent operations from the same clerk?

​	在本实验中, 我们并不允许来自同一个 clerk 的并发操作 (但是允许来自不同 clerk 实例的并发操作), 即同一个 clerk 实例应当总是在上一个操作返回后再继续进行下一个. 

#### how does clerk find the leader?

​	当 clerk 需要发起一个请求时, 首先需要找到正确的 leader. 方法是向所有的 server 发送一个 `GetState` RPC 请求, 该请求会返回自己是否是 leader 以及对应 的 term 的信息. clerk 找到具有最新 term 的 leader 为当前的 leader. 

​	但是上面的方法并不一定总是能找到集群最新的 leader. 例如, clerk 发起询问时, 当前 leader 刚好网络断连, 没有收到该询问, 同时集群中一个旧的 leader 接收到了该询问并返回自己的 term. 由于 clerk 没有收到新 leader 的回复, 所以认为 旧 leader 为当前的 leader. 

​	上面的情况并不会影响集群的安全性, 因为旧 leader 不可能将 command 复制到大部分节点, 也就不会进行 apply, 不会破坏集群的一致性.

#### `syncInfo` table

​	与 Lab2 相同, 每个 server 需要一个 `syncInfo` 保持 RPC 调用的串行性, 防止发生 RPC 的重复调用的问题. 

​	不同的是, `syncInfo` 需要在多个节点之间保持一致, 最好的办法就是将 RPC ID 与一个 log entry 进行绑定. 当 Follower 节点更新自己的 LCI 时, 会将自己的 `syncInfo` 对应的 RPC ID 更新到对应的 entry 相同.

##### clerk 一次操作流程

1. 群发 RPC 找到一个 leader, 发送对应操作的 RPC 到该 leader;

2. leader 检查 RPC ID, 若已经完成, 则在 `syncInfo` map 中会有对应的返回结果记录, 直接将对应的结果返回, 此次操作结束.

3. 若是一个新的 RPC, 则创建一个 `CmdEntry`, 并调用 `Raft.Start` 方法拟提交. 

4. 拟提交的操作并不代表已经提交, 若该 leader 非法, 或者在工作过程中出现了故障, 则本次操作很可能失败.

5. `applyCh` 中出现的结果是最终集群一致认同的操作, 因此每个节点需要一个数组维护所有在本节点拟提交的操作. 

   每个 server 节点有一条单独的 applier goroutine 用于接收 `applyCh` 提交的操作, 每出现一个操作, 则与本地数组头部元素的 `CmdIndex` 相比. 

   若相同, 则接着比较 `ClientID` 和 `RpcID`. 若同样相同, 说明该命令由本节点提交, applier goroutine 基于一个 channel 向等待的 RPC 回复 `true`. 

   否则一个有相同 `LogIndex` 但是内容不相同的命令被提交, 则说明本节点提交的命令失败, 则向 channel 回复 `false`.

##### 如何保持 `Client.RpcID` 的一致性?

​	在 Lab2 中通过 `RpcID` 保证 RPC 执行的有序性以及单次执行, 在分布式的情景下,  这个问题要变得更加复杂. 最重要的就是节点之间 `RpcID` 的接力问题. 

​	在 Raft 集群中, `RpcID` 应当总是由 leader 进行更新, 并随 `LogEntry` 分发到各节点. 对于 follower 节点的 Applier 而言, 当其 apply 某个 client 的 command 时, 将会将对应的 `RpcID` 加一. (当节点 crash 后重启时, 也可以通过这个方法回复)

​	当 leader 发生 crash 时, 很大概率其 logs 中还存在部分未来得及分发的 commands. 这里基于每个 clerk 只能串行提交命令的前提, 在这些 commands 中, 对于每个 clerk 最多只有一个来自该 clerk 的 command. 

​	对于新上任的 leader, 可以保证其 `Client.RpcID` 与旧 leader 均是一致的. 而在其没有 commit 的 log entries 中, 对于某个在上一个 term 时期提交了 command 的 clerk, 存在两种情况

1. 新 leader 包含该 log entry, 则该 command 可能最终会被提交, 此过程中 clerk 可能因为超时重传了相同 `RpcID` 的 RPC; 重传的时间点可能在 leader apply 该 RPC 之前或之后.

   1. 若在之后, 则 leader 会因为 `RpcID` 不匹配而直接返回上一次执行的结果, 该命令不会被分发;

   2. 若在之前, 则因为 leader 的 `RpcID` 没有更新, 所以当作一个新的 command 接受, 并且分发到各 follower. 

      但是 applier 同样会检查 log entry 中的 `RpcID`, 当不匹配时, 不会执行命令, 所以依然保证了不会执行两遍.

2. 新 leader 不包含该 log entry, 则 clerk 最终会超时重传, 此时到达的 command 对于新 leader 等同于一个新的 command, 并且 `RpcID` 可以匹配. 