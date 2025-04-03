# 使用说明
- 运行 build.sh 脚本打包
- 在管理后台 插件菜单 配置 redis 连接地址 url:post password database redisKey
- 在管理后台 AI菜单下绑定 UID ，和该 UID 私聊就会添加敏感词 再次发送相同文本就是删除敏感词
- 当然也可以不绑定 UID , 只要使用同一个 redis 同一个 database 在别的系统增加敏感词也可以