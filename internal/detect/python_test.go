package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// T15 Django：manage.py + 依赖里有 django → runserver 8000。
func TestT15DjangoManagePy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "Django==4.2.7\npsycopg2-binary==2.9.9\n")
	writeFile(t, dir, "manage.py", "#!/usr/bin/env python\nimport os\nimport sys\n\n"+
		"def main():\n    os.environ.setdefault('DJANGO_SETTINGS_MODULE', 'proj.settings')\n"+
		"    from django.core.management import execute_from_command_line\n"+
		"    execute_from_command_line(sys.argv)\n\n\nif __name__ == '__main__':\n    main()\n")

	s := only(t, scanOne(t, dir, nil))
	if s.Command != "python manage.py runserver 127.0.0.1:8000" {
		t.Fatalf("command 应为 python manage.py runserver 127.0.0.1:8000，实际 %q", s.Command)
	}
	if s.Port != 8000 {
		t.Fatalf("port 应为 8000，实际 %d", s.Port)
	}
	if s.Framework != "django" || s.Kind != KindPython {
		t.Fatalf("应标 django/python，实际 %q/%q", s.Framework, s.Kind)
	}
}

// T16 纯脚本项目（依赖里没有服务框架）→ 一条都不出。
func TestT16PythonLibraryOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "pyproject.toml", "[project]\nname = \"scraper\"\ndependencies = [\n  \"requests==2.31.0\",\n]\n")
	writeFile(t, dir, "main.py", "import requests\n\nif __name__ == '__main__':\n    print(requests.get('https://example.com').status_code)\n")

	none(t, scanOne(t, dir, nil))
}

// T17 FastAPI + 源码里的 port=8085。
func TestT17FastAPISourcePort(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "fastapi==0.110.0\nuvicorn==0.27.1\n")
	writeFile(t, dir, "main.py", "import uvicorn\nfrom fastapi import FastAPI\n\napp = FastAPI()\n\n"+
		"if __name__ == '__main__':\n    uvicorn.run(app, host='127.0.0.1', port=8085)\n")

	s := only(t, scanOne(t, dir, nil))
	if s.Port != 8085 || s.PortFrom != PortFromSource {
		t.Fatalf("port 应为 8085/source，实际 %d/%s", s.Port, s.PortFrom)
	}
	if s.Command != "python main.py" {
		t.Fatalf("command 应为 python main.py，实际 %q", s.Command)
	}
}

// T18 有服务框架依赖但找不到入口 → low + 空命令 + warning。
func TestT18PythonNoEntry(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "fastapi==0.110.0\n")
	writeFile(t, dir, "README.md", "# 只是个说明\n")

	s := only(t, scanOne(t, dir, nil))
	if s.Command != "" {
		t.Fatalf("判不出入口时 command 必须为空串，实际 %q", s.Command)
	}
	if s.Confidence != ConfidenceLow {
		t.Fatalf("confidence 应为 low，实际 %q", s.Confidence)
	}
	if len(s.Warnings) == 0 {
		t.Fatalf("low 建议必须带 warning，实际没有")
	}
}

// 额外护栏：依赖闸门按「词」匹配，requirements.txt 里的 flask-cors 之类
// 不能因为子串就把纯脚本目录误判成服务。
func TestPythonDependencyGateIsWordBased(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "requirements.txt", "toml==0.10.2\nnumpy==1.26.4\n")
	writeFile(t, dir, "main.py", "import toml\n\nif __name__ == '__main__':\n    print(toml.loads('a=1'))\n")
	none(t, scanOne(t, dir, nil))

	// 同一棵树，把一个真的服务框架写进去就应该产出了。
	writeFile(t, dir, "requirements.txt", "fastapi==0.110.0\n")
	if _, err := os.Stat(filepath.Join(dir, "main.py")); err != nil {
		t.Fatalf("main.py 应存在: %v", err)
	}
	s := only(t, scanOne(t, dir, nil))
	if s.Framework != "fastapi" {
		t.Fatalf("framework 应为 fastapi，实际 %q", s.Framework)
	}
}
