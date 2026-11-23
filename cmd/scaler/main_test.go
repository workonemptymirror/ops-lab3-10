package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDesiredReplicas(t *testing.T) {
	nan := math.NaN()
	for _, c := range []struct {
		current     int
		signal      float64
		lo, hi, out int
	}{
		{2, 1.16, 2, 6, 4}, // L-11 第 13 页的例子：⌈2 × 116 ÷ 70⌉ = 4
		{4, 0.75, 2, 6, 4}, // 75 ÷ 70 = 1.07，在 10% 容差内，不动
		{4, 0.35, 2, 6, 2}, // ⌈4 × 0.5⌉ = 2
		{2, 5.0, 2, 20, 6}, // 期望 15，但每次最多扩到 max(2 × 2, 2 + 4) = 6
		{3, 2.0, 2, 4, 4},  // 夹到上限
		{3, nan, 4, 6, 4},  // 取不到信号：保持当前数，但不低于最少数
	} {
		if got := desiredReplicas(c.current, c.signal, 0.7, 0.1, c.lo, c.hi); got != c.out {
			t.Errorf("当前 %d、信号 %v：期望 %d，得到 %d", c.current, c.signal, c.out, got)
		}
	}
}

func TestStabilized(t *testing.T) {
	now := time.Now()
	hist := []histEntry{{now.Add(-400 * time.Second), 6}, {now.Add(-100 * time.Second), 5}, {now.Add(-15 * time.Second), 3}}
	if got := stabilized(6, 2, hist, now, 300*time.Second); got != 5 {
		t.Errorf("缩容应取窗口内的最大值 5（400 s 前的 6 已过期），得到 %d", got)
	}
	if got := stabilized(2, 4, hist, now, 300*time.Second); got != 4 {
		t.Errorf("扩容不等稳定窗口，得到 %d", got)
	}
	if got := stabilized(4, 2, nil, now, 300*time.Second); got != 2 {
		t.Errorf("没有历史时直接缩，得到 %d", got)
	}
}

func TestSchedule(t *testing.T) {
	sched, err := parseSchedule("+400s=2, +20s=4")
	if err != nil {
		t.Fatal(err)
	}
	if len(sched) != 2 || sched[0].at != 20*time.Second || sched[0].min != 4 {
		t.Fatalf("应按时刻排序：%+v", sched)
	}
	for _, c := range []struct {
		since   time.Duration
		running bool
		want    int
	}{{10 * time.Second, true, 2}, {30 * time.Second, true, 4}, {500 * time.Second, true, 2}, {30 * time.Second, false, 2}} {
		if got := minAt(2, sched, c.since, c.running); got != c.want {
			t.Errorf("负载开始后 %v（running=%v）：最少数应为 %d，得到 %d", c.since, c.running, c.want, got)
		}
	}
	for _, bad := range []string{"20s", "+20s=0", "+x=3", "=3"} {
		if _, err := parseSchedule(bad); err == nil {
			t.Errorf("%q 应当报错", bad)
		}
	}
}

func TestParseUnitShowAndProm(t *testing.T) {
	st := parseUnitShow("ActiveState=failed\nResult=oom-kill\nExecMainStatus=9\nNRestarts=3\nLoadState=loaded\n")
	if st.active != "failed" || st.result != "oom-kill" || st.status != "9" || st.restarts != 3 {
		t.Errorf("parseUnitShow = %+v", st)
	}
	if st := parseUnitShow("ActiveState=inactive\nLoadState=not-found\n"); st.active != "" {
		t.Errorf("不存在的服务应返回空状态：%+v", st)
	}
	v, err := parsePromValue("vector", json.RawMessage(`[{"metric":{},"value":[1700000000.1,"0.84"]}]`))
	if err != nil || v != 0.84 {
		t.Errorf("vector：%v %v", v, err)
	}
	if v, err := parsePromValue("scalar", json.RawMessage(`[1700000000.1,"2"]`)); err != nil || v != 2 {
		t.Errorf("scalar：%v %v", v, err)
	}
	if _, err := parsePromValue("vector", json.RawMessage(`[{"value":[1,"1"]},{"value":[1,"2"]}]`)); err == nil {
		t.Error("多条序列应当报错")
	}
	if _, err := parsePromValue("vector", json.RawMessage(`[]`)); err == nil {
		t.Error("没有序列应当报错")
	}
}

// TestNodeSlotsGateAndWait 验证 O2 的核心：有空位直接进拉镜像；空位不足进入等待；
// 同一轮不重复发起等待；到点后一次性放行。用很短的延迟，不真等 240s。
func TestNodeSlotsGateAndWait(t *testing.T) {
	o := options{NodeSlots: 2, Data: t.TempDir(), Version: "v1", BasePort: 8101}
	o.nodeDelay, o.pull = 30*time.Millisecond, 5*time.Millisecond
	s, err := newScaler(o)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	a := s.create("v1", false, "t")
	b := s.create("v1", false, "t")
	if a.state != stPulling || b.state != stPulling {
		t.Errorf("有空位时应直接进拉镜像，得到 %s/%s", a.state, b.state)
	}
	if s.nodeSlots != 0 {
		t.Errorf("两个实例应耗尽空位，剩 %d", s.nodeSlots)
	}
	c := s.create("v1", false, "t") // 第 3 个：无空位
	if c.state != stPendingNode || !c.pendingNode {
		t.Errorf("空位不足应等待节点，得到 %s", c.state)
	}
	first := s.nodePrepUntil
	d := s.create("v1", false, "t") // 第 4 个：加入同一轮
	if !s.nodePrepUntil.Equal(first) {
		t.Error("同一轮不应重置等待时刻（不重复请求节点）")
	}
	if len(s.nodePending) != 2 {
		t.Errorf("应有 2 个在等待，得到 %d", len(s.nodePending))
	}
	s.nodeAdvance(time.Now()) // 未到点
	if c.state != stPendingNode {
		t.Error("等待未到点不应放行")
	}
	s.mu.Unlock()

	time.Sleep(40 * time.Millisecond)
	s.mu.Lock()
	s.nodeAdvance(time.Now()) // 到点
	if c.state != stPulling || d.state != stPulling {
		t.Errorf("节点就绪后应进入拉镜像，得到 %s/%s", c.state, d.state)
	}
	if len(s.nodePending) != 0 || !s.nodePrepUntil.IsZero() {
		t.Error("放行后应清空本轮")
	}
	s.mu.Unlock()
}

// TestNodeDisabledByDefault 验证默认关闭：不设 -node-slots 时所有实例直接进拉镜像，
// 且不生成 node-events.csv（保证对成员3/4/5 零影响）。
func TestNodeDisabledByDefault(t *testing.T) {
	dir := t.TempDir()
	o := options{NodeSlots: 0, Data: dir, Version: "v1", BasePort: 8101}
	o.pull = time.Second
	s, err := newScaler(o)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	for i := 0; i < 5; i++ {
		if in := s.create("v1", false, "t"); in.state != stPulling {
			t.Errorf("关闭节点模拟时第 %d 个应直接进拉镜像，得到 %s", i, in.state)
		}
	}
	s.mu.Unlock()
	if _, err := os.Stat(filepath.Join(dir, "node-events.csv")); !os.IsNotExist(err) {
		t.Error("关闭时不应生成 node-events.csv")
	}
}

// TestNodeSlotReleaseOnForget 验证空位归还与取消等待：占位实例离开后归还空位；
// 等待中的实例离开后清空本轮，不遗留任务。
func TestNodeSlotReleaseOnForget(t *testing.T) {
	o := options{NodeSlots: 1, Data: t.TempDir(), Version: "v1", BasePort: 8101}
	o.nodeDelay, o.pull = time.Millisecond, time.Millisecond
	s, err := newScaler(o)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	a := s.create("v1", false, "t") // 占掉唯一空位
	if s.nodeSlots != 0 {
		t.Fatalf("空位应被占用，剩 %d", s.nodeSlots)
	}
	b := s.create("v1", false, "t") // 无空位，等待
	if b.state != stPendingNode {
		t.Fatalf("应等待节点，得到 %s", b.state)
	}
	s.forget(a) // a 离开，归还空位
	if s.nodeSlots != 1 {
		t.Errorf("应释放空位，剩 %d", s.nodeSlots)
	}
	s.forget(b) // b 是等待中的，forget 后应清空本轮
	if len(s.nodePending) != 0 || !s.nodePrepUntil.IsZero() {
		t.Error("等待实例离开后应取消本轮")
	}
	s.mu.Unlock()
}
