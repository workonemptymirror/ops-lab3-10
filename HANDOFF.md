# HANDOFF.md —— 实验 3 累计交接记录（第 <组号> 组）

> 本文件按分工规则**累计维护**：代码提交号、已完成 run、报告位置、核绑定、工具版本、C/P/PLAN/2C、服务参数、终端与服务状态、下一位第一步。每人交接前**追加**自己的小节，不删除前面成员的内容；所有后续成员从本记录读取参数，不要求原作者重新计算或操作。
>
> 说明：成员 1 未留书面交接记录，本文件从成员 2 开始累计；任务 A/O1 的结果与参数以 `LAB3.md` 第 1、2 节及 `runs/` 对应目录为准。

## 成员 2（伸缩控制 B + O2）→ 成员 3 ｜ 2026-11-23

### 1. 完成状态与报告位置

- 任务 B 必做全部完成（同一条 PLAN、同一主机、连续进行）：

| run | 内容 | 结果一句话 |
|---|---|---|
| `runs/b0-one` | 单实例上限（rates 20~200，step 20s） | 上限 120 请求/秒（阶段 6 零错误，阶段 7 首次出错 16.1%） |
| `runs/b1-fixed` | 常驻 4 实例 | 缺口 0、错误 0、42.0 实例·分钟 |
| `runs/b2-cpu` | 反应式，CPU 信号 | 缺口 95 s（141–236）、错误 14.47%、36.9 实例·分钟 |
| `runs/b2-qps` | 反应式，QPS 信号（`-target 84`） | 缺口 111 s（141–252）、错误 15.84%、36.7 实例·分钟 |
| `runs/b3-sched` | 定时 + 反应式（`+20s=4,+480s=2`） | 缺口 0、错误 0、41.2 实例·分钟 |

- 选做 O2 完成：`runs/o2-node-wait`（含手动归档的 `node-events.csv`）；**短路径直接复用 `b2-cpu`，未重测**。
- 报告：`LAB3.md` 第 3.1～3.3 节、第 6 节问题 2、第 7 节 O2 行及 7.1 小节已由成员 2 写好。其余部分：第 1、2 节归成员 1；第 4、5 节、第 6 节问题 1/3/4、第 7 节其他行、第 8 节归各成员。
- 代码版本：commit `<待填：git rev-parse HEAD 完整 40 位>`（包含 cmd/scaler 的 O2 改动、全部 runs 与报告）。scaler 二进制编译于 2026-11-23 12:57，**b0-one 重测起所有 run 都用该版本**。
- **`runs/b2-cpu` 同时是 O2 的短路径和成员 5 O4 的 scaler 对照**（PLAN、配额 25%、结果见上表），不要重测、不要覆盖该目录。

### 2. 关键数字（后续成员直接使用，勿重算）

| 量 | 值 | 出处 / 算式 |
|---|---|---|
| 单实例上限（25% 配额） | **120 请求/秒** | `runs/b0-one/steps.csv` 阶段 6 |
| 容量线 C | **84** | 0.7 × 120 |
| 峰值 P | **340** | 4 × 84 = 336，取整到 10 |
| 平时到达率 | **34** | P ÷ 10 |
| **2C（任务 C/D 的恒定负载）** | **168** | 2 × 84；C1 用 `-plan 168@240s`，D2 用 `168@480s`（替换指导书示例里的 100） |
| PLAN 字符串 | `34@120s,34~340@30s,340@300s,340~34@60s,34@120s` | `PLAN=$(scripts/plan.sh 34 340)` |

- 自洽性：峰值 340 时 4 实例人均 85 ≈ C，恰好用满 max=4；2 实例人均 170 > 上限 120，不够用。
- `$PLAN` 是终端级变量：**每次开新终端 D 都要重新定义并 `echo "$PLAN"` 验证**，否则 run3.sh 报“缺少 -rates”。

### 3. 两个伸缩信号（终端 C 定义，**必须单引号**，`${quota}` 是留给 scaler 替换的字面量）

```bash
CPU='avg(rate(cgroup_cpu_usage_seconds_total{job="instances"}[15s])) / ${quota}'
QPS='sum(rate(lb_requests_total{backend!="none"}[15s])) / count(up{job="instances"} == 1)'
C=84
```

CPU 信号用默认 `-target 0.7`；QPS 信号必须配 `-target "$C"`（= 84，单位是每实例请求/秒）。

### 4. scaler 代码改动（O2，成员 3~5 必读）

- `cmd/scaler/main.go` 新增两个参数：`-node-slots`（初始节点空位数，**默认 0 = 关闭**）、`-node-delay`（空位不足时的等待时长，默认 0）。
- **关闭时行为与原版一致**：有单元测试 `TestNodeDisabledByDefault`，且 b1/b2/b3 全部实测于关闭状态。
- 测试：`go test ./cmd/scaler/` 共 7 个（原 4 + 新增 3 个 TestNode*）全部通过；`make build test` 全绿。
- 节点事件写入独立文件 `data/node-events.csv`（仅 `-node-slots>0` 时创建，不改动既有 CSV 语义）。o2 跑完后已 `rm -f data/node-events.csv`，归档副本在 `runs/o2-node-wait/node-events.csv`。
- **普通命令（成员 3~5 一律用这种，不要加 `-node-*`）**，例如任务 C：

```bash
sudo taskset -c 0 bin/scaler -mode fixed -replicas 3 -cpus 1
```

- 节点模拟命令（仅 O2 用过，留档供答辩复现，**其他人不要用**）：

```bash
sudo taskset -c 0 bin/scaler -mode reactive -min 2 -max 4 -cpus 1 -signal "$CPU" -node-slots 2 -node-delay 240s
```

- 踩坑备忘：启动 scaler 报 `127.0.0.1:8091: address already in use` = 旧 scaler 还在跑，用 `sudo pkill -x scaler`（**精确名字**；不要 `pkill -f lab3`，会连 lbd 一起杀），`ss -ltnp | grep 8091` 确认空后再启动；跑前 `sudo scripts/cleanup.sh` 清残留实例。

### 5. 分核与环境

- 实例：核 1（scaler `-cpus` 默认 "1"）；scaler / lbd / loadgen / Prometheus / node_exporter：核 0（`taskset -c 0`，终端 D 压测前 `export GEN_CPUS=0`）。
- 工具版本：Prometheus 3.13.4、node_exporter 1.12.1、Go `<待填：go version>`。
- SLO 口径：错误 = 失败请求；缺口秒 = 出错或 p99 > 200 ms 的秒；p99 只统计成功请求。
- 2 vCPU 主机上 loadgen 自身 lag_p99 ≈ 1.1 ms（各 run 的 steps.csv），相对 200 ms 阈值可忽略。

### 6. 交接时的服务状态（2026-11-23 约 16:20）

`<待填：清场完成后按实际核对/修改本节>`

- scaler 已 Ctrl-C 停止（自启实例随之停止）；已执行 `sudo scripts/cleanup.sh`；`systemctl list-units --all 'lab3-*'` 为空。
- 节点等待模拟已关闭（scaler 已停，后续命令不带 `-node-*` 即为关闭状态）。
- **lbd（终端 B）、Prometheus + node_exporter（终端 A）保持运行**，监控数据连续，勿重启。
- `runs/` 共 6 个目录：b0-one、b1-fixed、b2-cpu、b2-qps、b3-sched、o2-node-wait，均已 `git add` 提交。`<待填：提交后核对>`

### 7. 成员 3 的第一步

1. 核对本文件与 `runs/` 完整性；`git log -1` 确认 commit 已推送、哈希已回填。
2. 终端 C：`sudo taskset -c 0 bin/scaler -mode fixed -replicas 3 -cpus 1`，等 3 个实例就绪（`curl -s 127.0.0.1:8090/backends` 三个 up:true，约 85 s）。
3. 终端 D：`export GEN_CPUS=0`；C1 负载：`scripts/run3.sh c1-stop -url http://127.0.0.1:8080/api/courses/1 -plan 168@240s`。
4. 按分工，成员 2 此后不再返回参加 O3/O4；有疑问看本文件与 `LAB3.md` 第 3 节、7.1 节。

## 成员 3（任务 C1/C2）→ 成员 4 ｜ 2026-11-24

1. 完成状态与报告位置
任务 C1（停止与卡住）、C2（暂停）已完成。
数据目录：`runs/c1-stop/`、`runs/c2-default/`、`runs/c2-sensitive/`。
报告：`LAB3.md` 第 4 节、第 6 节问题 3 已填。

2. 关键数字（C3 配额计算依据）
在 C1 负载 168 RPS（3 实例）下，从 Prometheus 查得单实例 CPU 用量 U ≈ 0.14 core。
建议的 C3 慢速配额 = 100 × 0.14 / 0.85 ≈ 16%（取整 15% 或 16%）。

3. 交接时的服务状态
- lbd 已恢复默认参数（无 -outlier，检查间隔 1s）。
- scaler 以 `-mode fixed -replicas 3` 运行，3 个实例（8101, 8102, 8103）均 up。
- 当前无负载压测。

4. 成员 4 的第一步
- 先确认 `curl -s 127.0.0.1:8090/backends` 显示 3 个 up。
- 按 C3 指导书，使用 U=0.14, 配额 16% 进行注入：`sudo scripts/inject.sh slow 8102 16`。
