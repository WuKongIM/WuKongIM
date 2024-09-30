
# 打包helm
``` bash
helm package ./wukongim-helm
```

# 安装
```bash
helm install test 
```
# 卸载
```bash
helm uninstall test
```
# 回滚版本
```bash
helm rollback test 1
```

# 部署

```bash
# externalIP 外部访问IP
# replicaCount 节点数(副本)
# namespace 命名空间
helm install mytestwukongim ./ --set externalIP=121.41.92.15 replicaCount=5 namespace=demo
```