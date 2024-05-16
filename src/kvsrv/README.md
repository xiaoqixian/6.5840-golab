#### Lab2 Key/Value Server

​	第二个 Lab 需要实现一个并发的键值对数据库服务器, 需要在不同客户端的请求之间保持串行一致性, 即服务器应该表现为如同客户端的请求按照某种特定的顺序到达. 可以采用多版本并发控制.

​	服务端需要维护两张表, 一张是数据表, 一张是客户端的同步信息表.

1. 数据表 `data`: `sync.Map` 类型, `key` 即为实际的客户传过来的键值, `value` 为如下的结构体的指针类型:

   ```go
   type Value struct {
     value string
     sync.RWMutex
   }
   ```

   从结构体上看, 数据表采用读写锁来保证数据读写的原子性. 因为 Go 没有类似 `fetch_add` 之类对新值进行操作后获取旧值的原子操作, 否则可以直接使用原子变量.

2. 同步信息表 `clientsSync`: 用于存储客户端的同步信息, 之后会有更多介绍.

#### RPC 同步

​	实验的主要难点在于如何解决不稳定网络下的 RPC 重复调用问题. 为了保证每个 RPC 调用只被执行一次, 需要对每个 RPC 请求进行编号. 

1. 客户端维护一个从 1 开始单调递增的 `RpcID` , 每次发送一个新的 RPC 请求时将该 `RpcID` 加1后得到该 RPC 的 `RpcID`, 如果该请求是因为超时重传就不需要更新 `RpcID`. 

2. 服务端维护一张从客户端 ID 映射到一个如下结构体的 map

   ```go
   type SyncInfo struct {
   	RpcID uint32
     // the result of the last rpc operation.
   	LastValue string
   }
   ```

服务端确立以下规则:

1. 服务端只会执行对应 client 的 `SyncInfo.RpcID` 与 RPC 请求中的 `args.RpcID` 的 RPC 请求;

2. 若 `args.RpcID < SyncInfo.RpcID`, 则说明该请求是重复请求, 已经执行过了, 但是服务端依然表现得如同成功响应. 

   `SyncInfo.LastValue` 存储有最近一次 RPC 操作的结果, 服务端则会直接将该结果返回给客户端. 同时 `SyncInfo.RpcID-1` 返回给客户端, 等于客户端需要将自己的 `RpcID` 更新到该值 (因为客户端在新发起一个 RPC 请求时自动将 `RpcID` 加一, 所以需要减一).

3. 正常情况下不可能出现 `args.RpcID > SyncInfo.RpcID`, 若出现则说明出现了程序编写错误. 

上述的规则确保了服务端在不稳定网络下对于同一个 RPC 永远只会执行第一个到达的. 

#### 并发

1. 使用 `sync.Map` 提供 K/V 表的并发读写;

2. 对于单个表项需要使用读写锁来提供并发读写

   ```go
   type Value struct {
   	value string
   	sync.RWMutex
   }
   ```

   对于 `Value` 进行读写时需要先抢占读写锁.
