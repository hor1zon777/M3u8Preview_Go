package plugin

import "testing"

// fakePlugin 测试用最小实现。
type fakePlugin struct {
	id      string
	enabled bool
}

func (f *fakePlugin) Meta() Meta                { return Meta{ID: f.id, Name: "fake-" + f.id} }
func (f *fakePlugin) Enabled() bool             { return f.enabled }
func (f *fakePlugin) SetEnabled(v bool) error   { f.enabled = v; return nil }
func (f *fakePlugin) Status() Status            { return Status{Healthy: true} }

func TestRegistryRegisterAndGet(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(&fakePlugin{id: "a"}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(&fakePlugin{id: "b"}); err != nil {
		t.Fatalf("register b: %v", err)
	}

	if _, ok := r.Get("a"); !ok {
		t.Fatalf("Get(a) 应命中")
	}
	if _, ok := r.Get("missing"); ok {
		t.Fatalf("Get(missing) 不应命中")
	}
}

func TestRegistryRejectsInvalid(t *testing.T) {
	r := NewRegistry()

	if err := r.Register(nil); err == nil {
		t.Fatalf("register nil 应报错")
	}
	if err := r.Register(&fakePlugin{id: ""}); err == nil {
		t.Fatalf("register 空 ID 应报错")
	}
	if err := r.Register(&fakePlugin{id: "dup"}); err != nil {
		t.Fatalf("register dup 首次应成功: %v", err)
	}
	if err := r.Register(&fakePlugin{id: "dup"}); err == nil {
		t.Fatalf("register 重复 ID 应报错")
	}
}

// List 必须保持注册顺序（决定前端卡片顺序），且返回副本。
func TestRegistryListOrder(t *testing.T) {
	r := NewRegistry()
	ids := []string{"c", "a", "b"}
	for _, id := range ids {
		if err := r.Register(&fakePlugin{id: id}); err != nil {
			t.Fatalf("register %s: %v", id, err)
		}
	}

	got := r.List()
	if len(got) != len(ids) {
		t.Fatalf("List 长度 = %d, 期望 %d", len(got), len(ids))
	}
	for i, p := range got {
		if p.Meta().ID != ids[i] {
			t.Fatalf("List[%d] = %s, 期望 %s（应保持注册顺序）", i, p.Meta().ID, ids[i])
		}
	}

	// 修改返回切片不应影响内部状态
	got[0] = &fakePlugin{id: "hacked"}
	if r.List()[0].Meta().ID != "c" {
		t.Fatalf("List 应返回副本，内部顺序被外部修改污染")
	}
}
