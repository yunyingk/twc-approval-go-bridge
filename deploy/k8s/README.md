# WSL kind 演练

当前真实业务继续由原 Docker Compose 服务运行。本目录用同一份 Go 镜像验证 Kubernetes 部署；演练使用合成表结构、关闭识别、关闭通知、手动审核且不配置任何飞书凭据。不会读取真实 `configs/config.toml` 或迁移业务数据。

## 简单入口

在 Mac 的仓库目录执行，不需要 Mac Docker Desktop：

```bash
make k8s-lab-up          # 创建／恢复测试集群
make k8s-lab-deploy      # 部署最近选择的测试成品，首次默认现有 remote-build 成品
make k8s-lab-status      # 查看节点预算、版本、Pod 和 HTTP 状态
make k8s-lab-verify      # 实测 Pod 重建后数据保留及只读账号权限
make k8s-lab-rollback    # 回到上一个已验证的测试成品
make k8s-lab-stop        # 停止测试节点，保留测试数据
```

选择另一成品：`make k8s-lab-deploy ARTIFACT=/绝对路径/成品目录`。成品由现有 `make remote-build` 生成，或由下述通用构建入口产生。测试选择保存在忽略目录 `data/k8s-lab/latest.json`，不改 Docker 发布工具的默认成品选择。

WSL 私有目录 `/home/dev/apps/twc-k8s-lab`；工具为 kind v0.33.0、kubectl/Kubernetes v1.34.11，下载校验值固定在管理脚本中，节点镜像按官方摘要固定。节点 CPU 上限 2、内存及 memory+swap 上限 4 GiB，并非预占全部资源。只操作 `kind-twc-lab` 上下文和 `twc-lab` 项目空间，集群 API 和 HTTP 都只发布到 WSL localhost 随机端口；状态显示的 URL 不能直接当作 Mac localhost 使用。

## 验证内容

`base/` 为普通 Kubernetes 基础清单，保持单副本和 Recreate，持久化 `/data`，探针为 `/healthz`、`/readyz`。配置改为 Secret 投射（0440，仅运行组可读），Pod 非 root、只读根目录、禁止提权和额外 capabilities，数据卷仍可写。实际源配置文件仍按项目要求保管，不把凭据提交 Git。生产如启用自有模型规则，需由运维把对应规则文件额外挂载到配置引用位置；本演练不包含真实模型规则或凭据。

`overlays/lab/` 替换 Namespace、1Gi PVC 和 NodePort，移除公网 Ingress。1Gi 是本地存储申请值，不是 local-path 驱动的磁盘硬限制。Namespace 采用受限 Pod 策略；只读 viewer 可以看本空间 Pod，但不能读 Secret 或查看其他空间的 Pod。验证器只在合成 PVC 内写入哨兵文件，再重建应用 Pod 检查保留情况。

kind/containerd 可能为导入的镜像再生成索引，Pod 的 imageID 不一定直接等于平台摘要。验证器校验每一层索引内容摘要并解析到原平台 manifest，不以字符串不同为理由跳过完整性校验。

## WSL 和 GitLab 使用同一构建核心

现有 Mac 入口 `make remote-build` 在 WSL 调用 `scripts/remote-service.py` 的构建核心。新增 `scripts/build-image.py` 在 Linux 本机直接调用同一核心，无需 SSH、业务凭据或 Go 安装；编译、Go 双构建测试和 go vet 在 Docker 构建阶段执行。

公司 Linux Shell Runner 或有 Git checkout 的 WSL 目录可执行：

```bash
python3 -B scripts/build-image.py
python3 -B scripts/publish-image.py data/ci-artifacts/<commit> <镜像仓库/项目/仓库>
```

发布命令读取已验证成品，需事先通过公司规定方式登录镜像仓库；不会在发布时重新构建。每次输出固定提交／构建摘要的标签及 `registry.json` 中的不可变拉取引用。Mac 仅通过 SSH 管理 WSL；不要在 Mac 本机执行要求 Docker 的 `make image-build`。

根目录 `.gitlab-ci.yml` 是需要公司参数接入的模板，假定专用非 root Linux Shell Runner 已配置 Python 3.11+、Git、Docker 权限及网络；`BUILD_RUNNER_TAG` 是选择 Runner 的参数，不是程序安全规则。构建任务保存镜像成品。发布任务仅默认分支且仓库变量齐备时允许手工触发；凭据通过 masked/protected CI variables 提供，不写入镜像或 artifacts。公司若使用 Docker/Kubernetes executor，需要按运维允许的构建方式适配，不能宣称任意 Runner 都可直接运行。

本地演练和 Shell Runner 入口验证不等于公司 GitLab、ACR 或 ACK 实际验收。生产侧还需指定镜像仓库／权限、Namespace、IngressClass（ACK 可能采用 ALB）、域名／证书、StorageClass、Secret 和资源配额。本轮不接入公司环境、不安装公网入口，也不搬迁现有 WSL 服务。
