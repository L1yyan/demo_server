# demo_server
这是一个FPS游戏的服务端，logic是处理玩家信息。match负责匹配流程。roomserver就是局内了。
大体流程就是logic 发match请求 - match开始匹配，匹配到房间返回一个roomtoken给logic 然后客户端带着token kcp直连到roomserver 这样就开始玩耍了。具体的流程呢，都在里面^_^
