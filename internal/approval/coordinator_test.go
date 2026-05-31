package approval

import (
	"testing"
	"time"
)

func TestKeyComposition(t *testing.T) {
	if Key("r1", "s1") != "r1|s1" {
		t.Errorf("Key = %q", Key("r1", "s1"))
	}
}

func TestResolveDelivers(t *testing.T) {
	c := New()
	key := Key("r1", "s1")
	ch := c.Wait(key)
	if !c.IsWaiting(key) {
		t.Fatal("应在等待")
	}
	go func() {
		// 给主协程时间进入接收。
		time.Sleep(5 * time.Millisecond)
		if !c.Resolve(key, Decision{Approved: true, Actor: "admin"}) {
			t.Errorf("Resolve 应成功")
		}
	}()
	select {
	case d := <-ch:
		if !d.Approved || d.Actor != "admin" {
			t.Errorf("决定不符:%+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("超时未收到决定")
	}
	if c.IsWaiting(key) {
		t.Error("决定后应移除等待者")
	}
}

func TestResolveNoWaiter(t *testing.T) {
	c := New()
	if c.Resolve(Key("x", "y"), Decision{Approved: true}) {
		t.Error("无等待者时 Resolve 应返回 false")
	}
}

func TestResolveTwiceSecondFails(t *testing.T) {
	c := New()
	key := Key("r", "s")
	ch := c.Wait(key)
	if !c.Resolve(key, Decision{Approved: true, Actor: "a"}) {
		t.Fatal("首次 Resolve 应成功")
	}
	<-ch
	if c.Resolve(key, Decision{Approved: false, Actor: "b"}) {
		t.Error("二次 Resolve 应失败(同门只决一次)")
	}
}

func TestCancelRemovesWaiter(t *testing.T) {
	c := New()
	key := Key("r", "s")
	c.Wait(key)
	c.Cancel(key)
	if c.IsWaiting(key) {
		t.Error("Cancel 后不应仍在等待")
	}
	if c.Resolve(key, Decision{}) {
		t.Error("Cancel 后 Resolve 应失败")
	}
	c.Cancel(key) // 幂等
}

func TestPendingKeys(t *testing.T) {
	c := New()
	c.Wait(Key("r1", "s1"))
	c.Wait(Key("r2", "s2"))
	if len(c.PendingKeys()) != 2 {
		t.Errorf("PendingKeys = %v", c.PendingKeys())
	}
}
