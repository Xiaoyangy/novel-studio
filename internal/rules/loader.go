package rules

import (
	"os"
	"path/filepath"
)

// LoadOptions 枚举 rules 文件来源目录，供 RawFileSources 扫描归一化。
//
// 目录不存在不算错误，扫描时静默跳过。
type LoadOptions struct {
	// HomeRulesDir 是 ~/.novel-studio/rules/ 目录；扫描其下所有顶层 .md（文件名字典序合并）。空表示跳过。
	HomeRulesDir string

	// ProjectRulesDir 是 ./.novel-studio/rules/ 目录（镜像全局，同样扫描其下所有顶层 .md）。空表示跳过。
	ProjectRulesDir string
}

// novelStudioDirName 是 novel-studio 在 user / project 两级共用的 dotdir 名。
// 全局 ~/.novel-studio/rules/ 与项目 ./.novel-studio/rules/ 由此对称。
const novelStudioDirName = ".novel-studio"

// DefaultProjectRulesDir 拼出 ./.novel-studio/rules/ 的绝对路径（基于给定项目目录）。
// 调用方传入项目根，避免在 loader 内部依赖 cwd；镜像 DefaultHomeRulesDir。
func DefaultProjectRulesDir(projectDir string) string {
	if projectDir == "" {
		return ""
	}
	return filepath.Join(projectDir, novelStudioDirName, "rules")
}

// DefaultHomeRulesDir 拼出 ~/.novel-studio/rules/ 目录的绝对路径。
// home 解析失败返回空串（调用方据此跳过该来源）。
func DefaultHomeRulesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, novelStudioDirName, "rules")
}

// homeRulesReadme 是首次引导时写入 ~/.novel-studio/rules/README.txt 的说明。
// 刻意用 .txt 后缀而非 .md——扫描只认 .md，这份说明不会被当成规则归一化。
const homeRulesReadme = `这里放全局写作偏好，跨所有书生效。

新建一个 .md 文件（如 my-style.md），用大白话写要求就行——
不需要任何格式、不需要 YAML：

    # 角色
    - 主角林尘别写成圣母，外冷内热即可
    # 风格
    - 多用身体感知（指节发白）替代情绪标签（紧张）
    - 对话别太书面，每章 3000 字左右
    - 不要出现"某种程度上"这种 AI 腔

写完不用管格式：系统会用模型把这些自然语言要求归一化成结构化约束
（字数范围、禁用词、疲劳词阈值等），写作时自动遵循、提交时自动自检。

多个 .md 按文件名字典序合并；点开头的隐藏文件、非 .md 文件都会被忽略
（所以这份 README.txt 不会被当成规则）。

常见 AI 套句、疲劳词的机械基线已内置，开箱即用，不写也没关系。

加载优先级（高 → 低）：./.novel-studio/rules/*.md（本书） > ~/.novel-studio/rules/*.md（这里） > 内置默认
`

// EnsureHomeRulesDir 尽力创建 ~/.novel-studio/rules/ 目录并写入 README.txt 引导，
// 让用户发现这个全局偏好扩展点、知道怎么写。
// nice-to-have，非关键路径：home 解析失败或写入出错都静默吞掉，绝不阻断启动。
func EnsureHomeRulesDir() {
	if dir := DefaultHomeRulesDir(); dir != "" {
		_ = ensureRulesDirAt(dir)
	}
}

// ensureRulesDirAt 创建目录并把 README.txt 写成当前引导模板，是 EnsureHomeRulesDir 的可测内核。
// README.txt 是系统生成的引导文件（用户偏好写在 *.md，它不被扫描加载），每次都覆盖为
// 最新模板——不保留旧内容，也就不需要任何版本兼容逻辑。
func ensureRulesDirAt(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "README.txt"), []byte(homeRulesReadme), 0o644)
}

// DefaultOptions 根据当前工作目录构造常用 LoadOptions。
//
// 适合 Host 启动时调用一次，让用户规则服务复用同一份来源配置。
// 解析 cwd 失败时 ProjectRulesDir 留空（扫描会跳过该来源）。
//
// 路径语义：ProjectRulesDir 绑定 **当前工作目录（cwd）** 而非 outputDir。
// 用户 cd 到不同目录启动写不同的书，./.novel-studio/rules/ 自然跟着 cwd 走；如需跨书共享，
// 放 ~/.novel-studio/rules/ 全局目录即可（其下所有 .md 都会被加载）。
func DefaultOptions() LoadOptions {
	cwd, _ := os.Getwd()
	return LoadOptions{
		HomeRulesDir:    DefaultHomeRulesDir(),
		ProjectRulesDir: DefaultProjectRulesDir(cwd),
	}
}
