// semver.go 版本号比较的薄封装。
// 统一规整为带 v 前缀后交给 golang.org/x/mod/semver（含 prerelease 语义）。
package update

import "golang.org/x/mod/semver"

// normalizeVersion 把 "0.3.0" 规整为 "v0.3.0"；非法 semver 返回空串。
// 本地 go run 的 "dev" 构建会落在非法分支——自更新对 dev 构建整体禁用。
func normalizeVersion(v string) string {
	if v == "" {
		return ""
	}
	if v[0] != 'v' {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

// versionNewer 报告 a 是否严格新于 b（两者可不带 v 前缀）。
// 任一非法时返回 false——比较不了就不做任何"更新"动作，宁可保守。
func versionNewer(a, b string) bool {
	va, vb := normalizeVersion(a), normalizeVersion(b)
	if va == "" || vb == "" {
		return false
	}
	return semver.Compare(va, vb) > 0
}
