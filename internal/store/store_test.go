package store

import (
	"os"
	"path/filepath"
	"testing"

	"devdock/internal/config"
	"devdock/internal/model"
)

// newTestStore 在临时目录里建一个独立配置存储，避免污染真实 %APPDATA%。
func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	st, err := New(cfg)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	return st, dir
}

// sampleProject 构造一个合法项目。cwd 用临时目录保证存在。
func sampleProject(t *testing.T, id, name string) model.Project {
	t.Helper()
	return model.Project{
		ID:      id,
		Name:    name,
		Cwd:     t.TempDir(),
		Command: "echo hello",
	}
}

// TestCreateAndGet 验证创建后能按 ID 与名称取回。
func TestCreateAndGet(t *testing.T) {
	st, _ := newTestStore(t)
	p := sampleProject(t, "", "前端 dev")

	created, err := st.Create(p)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if created.ID == "" {
		t.Fatal("应自动生成 ID")
	}

	byID, err := st.Get(created.ID)
	if err != nil {
		t.Fatalf("按 ID 查找失败: %v", err)
	}
	if byID.Name != "前端 dev" {
		t.Fatalf("名称不匹配: %s", byID.Name)
	}

	// 中文名称为空 slug 时会回退为 project-<数字>，仍应能按名称取回。
	if _, err := st.Get("前端 dev"); err != nil {
		t.Fatalf("按名称查找失败: %v", err)
	}
}

// TestCreateDuplicateID 验证重复 ID 被拒绝。
func TestCreateDuplicateID(t *testing.T) {
	st, _ := newTestStore(t)
	p := sampleProject(t, "dup", "dup1")
	if _, err := st.Create(p); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	p2 := sampleProject(t, "dup", "dup2")
	if _, err := st.Create(p2); err == nil {
		t.Fatal("重复 ID 应当被拒绝")
	}
}

// TestCreateValidation 验证非法输入被拒绝。
func TestCreateValidation(t *testing.T) {
	st, _ := newTestStore(t)

	cases := []struct {
		name string
		p    model.Project
	}{
		{"空名称", model.Project{Cwd: t.TempDir(), Command: "x"}},
		{"空命令", model.Project{Name: "n", Cwd: t.TempDir()}},
		{"多行命令", model.Project{Name: "n", Cwd: t.TempDir(), Command: "a\r\nb"}},
		{"相对目录", model.Project{Name: "n", Cwd: `relative\path`, Command: "x"}},
		{"目录不存在", model.Project{Name: "n", Cwd: `Z:\no\such\dir`, Command: "x"}},
		{"端口越界", model.Project{Name: "n", Cwd: t.TempDir(), Command: "x", Port: 70000}},
		{"就绪路径无端口", model.Project{Name: "n", Cwd: t.TempDir(), Command: "x",
			Readiness: &model.ReadinessConfig{Path: "/health"}}},
		{"就绪路径无斜杠", model.Project{Name: "n", Cwd: t.TempDir(), Command: "x", Port: 8080,
			Readiness: &model.ReadinessConfig{Path: "health"}}},
	}
	for _, c := range cases {
		if _, err := st.Create(c.p); err == nil {
			t.Errorf("%s：应当被拒绝但通过了", c.name)
		}
	}
}

// TestUpdateFields 验证部分更新只改指定字段。
func TestUpdateFields(t *testing.T) {
	st, _ := newTestStore(t)
	created, err := st.Create(sampleProject(t, "upd", "原名"))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	newName := "新名"
	newPort := 8080
	updated, err := st.Update("upd", Patch{Name: &newName, Port: &newPort})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if updated.Name != "新名" {
		t.Fatalf("名称未更新: %s", updated.Name)
	}
	if updated.Port != 8080 {
		t.Fatalf("端口未更新: %d", updated.Port)
	}
	// 未指定的字段必须保持原值。
	if updated.Command != created.Command {
		t.Fatalf("未指定的命令字段被改动了: %s", updated.Command)
	}
	if updated.Cwd != created.Cwd {
		t.Fatalf("未指定的目录字段被改动了: %s", updated.Cwd)
	}
}

// TestUpdateClearsReadiness 验证可以把就绪配置显式清空。
func TestUpdateClearsReadiness(t *testing.T) {
	st, _ := newTestStore(t)
	p := sampleProject(t, "rdy", "rdy")
	p.Port = 8080
	p.Readiness = &model.ReadinessConfig{Path: "/health"}
	if _, err := st.Create(p); err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	var empty *model.ReadinessConfig
	updated, err := st.Update("rdy", Patch{Readiness: &empty})
	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if updated.Readiness != nil {
		t.Fatalf("就绪配置未被清空: %+v", updated.Readiness)
	}
}

// TestDelete 验证删除后查不到。
func TestDelete(t *testing.T) {
	st, _ := newTestStore(t)
	if _, err := st.Create(sampleProject(t, "del", "del")); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	if _, err := st.Delete("del"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if _, err := st.Get("del"); err == nil {
		t.Fatal("删除后仍能查到")
	}
}

// TestPersistenceAcrossReload 是持久化的核心测试：
// 新建 store 实例后项目必须还在。
func TestPersistenceAcrossReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	cfg1, err := config.Load()
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	st1, err := New(cfg1)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	created, err := st1.Create(sampleProject(t, "persist", "持久化"))
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 配置文件必须真的落盘。
	cfgPath := filepath.Join(dir, "DevDock", "config.json")
	if _, err := os.Stat(cfgPath); err != nil {
		t.Fatalf("配置文件未写入磁盘: %v", err)
	}

	// 重新加载，模拟 DevDock 重启。
	cfg2, err := config.Load()
	if err != nil {
		t.Fatalf("重新加载配置失败: %v", err)
	}
	st2, err := New(cfg2)
	if err != nil {
		t.Fatalf("重新创建 store 失败: %v", err)
	}

	got, err := st2.Get(created.ID)
	if err != nil {
		t.Fatalf("重启后项目丢失: %v", err)
	}
	if got.Name != "持久化" {
		t.Fatalf("重启后名称不一致: %s", got.Name)
	}
	if got.Command != created.Command {
		t.Fatalf("重启后命令不一致: %s", got.Command)
	}
}

// TestCorruptConfigDegrades 验证配置损坏时不阻止启动。
func TestCorruptConfigDegrades(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)

	cfgDir := filepath.Join(dir, "DevDock")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("创建目录失败: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.json")
	if err := os.WriteFile(cfgPath, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatalf("写入损坏配置失败: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("配置损坏不应导致加载失败: %v", err)
	}
	if cfg.Warning() == "" {
		t.Fatal("配置损坏时应产生警告")
	}
	// 应能正常创建 store 且项目列表为空。
	st, err := New(cfg)
	if err != nil {
		t.Fatalf("配置损坏后创建 store 失败: %v", err)
	}
	if len(st.List()) != 0 {
		t.Fatalf("降级后应为空列表，实际 %d 项", len(st.List()))
	}
	// 原文件应被备份。
	matches, _ := filepath.Glob(cfgPath + ".corrupt-*")
	if len(matches) == 0 {
		t.Fatal("损坏的配置应被备份")
	}
}

// TestListOrderStable 验证列表按创建时间稳定排序。
func TestListOrderStable(t *testing.T) {
	st, _ := newTestStore(t)
	for _, id := range []string{"a", "b", "c"} {
		if _, err := st.Create(sampleProject(t, id, id)); err != nil {
			t.Fatalf("创建 %s 失败: %v", id, err)
		}
	}
	list := st.List()
	if len(list) != 3 {
		t.Fatalf("应有 3 个项目，实际 %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i].CreatedAt.Before(list[i-1].CreatedAt) {
			t.Fatal("列表未按创建时间排序")
		}
	}
}

// TestCloneIsolation 验证取出的项目是副本，改动它不会污染 store。
func TestCloneIsolation(t *testing.T) {
	st, _ := newTestStore(t)
	p := sampleProject(t, "clone", "clone")
	p.Env = map[string]string{"K": "V"}
	if _, err := st.Create(p); err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	got, err := st.Get("clone")
	if err != nil {
		t.Fatalf("查找失败: %v", err)
	}
	got.Env["K"] = "MUTATED"

	again, err := st.Get("clone")
	if err != nil {
		t.Fatalf("再次查找失败: %v", err)
	}
	if again.Env["K"] != "V" {
		t.Fatal("修改返回值污染了 store 内部状态")
	}
}
