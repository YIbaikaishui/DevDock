package detect

import (
	"os"
	"path/filepath"
	"strings"
)

// detectRust 实现 SPEC 4.6 的兜底规则：Cargo.toml + src/main.rs → cargo run（low）。
//
// JVM（pom.xml / build.gradle）与 docker compose 在 v1 一律不产出（SPEC A6）：
// 本机零验证样本，宁缺勿滥。build.gradle 只用于排除 android/ 这类移动端构建脚本。
func detectRust(p *dirProbe) *Suggestion {
	if !p.has("cargo.toml") {
		return nil
	}
	if !fileExists(filepath.Join(p.dir, "src", "main.rs")) {
		return nil
	}
	return &Suggestion{
		Cwd:         p.dir,
		Kind:        KindRust,
		Command:     "cargo run",
		CommandFrom: "Cargo.toml + src/main.rs",
		Confidence:  ConfidenceLow,
		Warnings:    []string{"Cargo 项目一般是命令行/桌面程序，默认没有网页端口，端口请自行填写"},
	}
}

// isAndroidGradle 判断一个 gradle 工程是不是移动端构建（这类不能当 JVM 后端）。
// 目前 detect 不产出 JVM 建议，保留它是为了让「为什么 gradle 不算项目」有据可查。
func isAndroidGradle(p *dirProbe) bool {
	if strings.EqualFold(filepath.Base(p.dir), "android") {
		return true
	}
	for _, name := range []string{"build.gradle", "build.gradle.kts"} {
		if text, ok := p.read(name); ok && strings.Contains(text, "com.android.application") {
			return true
		}
	}
	return false
}

// fileExists 是 os.Stat 的布尔封装。
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
