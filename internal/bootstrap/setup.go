package bootstrap

import (
	"bufio"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/chenhongyang/novel-studio/internal/rules"
	"github.com/chenhongyang/novel-studio/internal/utils"
)

// exampleConfig 是引导后写入 ~/.novel-studio/config.example.jsonc 的带注释模板。
// 嵌入文件必须与仓库根目录 config.example.jsonc 保持一致，测试会防止漂移。
//
//go:embed config.example.jsonc
var exampleConfig string

// NeedsSetup 检查是否需要首次引导（配置文件不存在时触发）。
func NeedsSetup(flagPath string) bool {
	if flagPath != "" {
		_, err := os.Stat(flagPath)
		return os.IsNotExist(err)
	}
	if p := DefaultConfigPath(); p != "" {
		if _, err := os.Stat(p); err == nil {
			return false
		}
	}
	if _, err := os.Stat(projectConfigPath()); err == nil {
		return false
	}
	return true
}

type setupProvider struct {
	name           string
	label          string
	baseURL        string // 预填的 base_url
	needType       bool   // 自定义代理需要额外问 type 和 base_url
	apiKeyOptional bool   // true 表示 API Key 允许留空
}

var setupProviders = []setupProvider{
	{name: "openrouter", label: "OpenRouter", baseURL: "https://openrouter.ai/api/v1"},
	{name: "anthropic", label: "Anthropic"},
	{name: "gemini", label: "Gemini"},
	{name: "openai", label: "OpenAI"},
	{name: "deepseek", label: "DeepSeek"},
	{name: "qwen", label: "Qwen"},
	{name: "glm", label: "GLM"},
	{name: "grok", label: "Grok"},
	{name: "ollama", label: "Ollama", baseURL: "http://localhost:11434/v1", apiKeyOptional: true},
	{name: "bedrock", label: "Bedrock", apiKeyOptional: true},
	{name: "custom", label: "Custom Proxy", needType: true, apiKeyOptional: true},
}

// RunSetup 运行首次引导，返回生成的配置。纯文本 stdin 交互，无 TUI 依赖，
// 因此在管道/重定向场景下也能逐行回答（无 TTY 时由调用方提前拦截）。
func RunSetup() (Config, error) {
	r := bufio.NewReader(os.Stdin)

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "未检测到配置文件，开始初始化设置...")
	fmt.Fprintf(os.Stderr, "  配置文件路径：%s\n", DefaultConfigPath())
	fmt.Fprintf(os.Stderr, "  完成后可随时编辑该文件调整高级设置。\n")
	fmt.Fprintln(os.Stderr)

	// Step 1: 选择 Provider
	sp, err := runProviderSelect(r, "[1/4] 选择 Provider", setupProviders)
	if err != nil {
		return Config{}, err
	}

	providerName := sp.name
	var pc ProviderConfig
	printStepDone("Provider", sp.label)

	// 自定义代理：额外问名称和 API 协议类型
	if sp.needType {
		providerName, err = runTextInput(r, "Provider 名称", "my-proxy")
		if err != nil {
			return Config{}, err
		}
		providerType, err := runProviderSelect(r, "API 协议类型", apiTypeOptions)
		if err != nil {
			return Config{}, err
		}
		pc.Type = providerType.name
	}

	// Step 2: 输入 API Key
	var apiKey string
	if sp.apiKeyOptional {
		apiKey, err = runOptionalTextInput(r, "[2/4] API Key（可留空）")
	} else {
		apiKey, err = runTextInput(r, "[2/4] API Key", "sk-xxx")
	}
	if err != nil {
		return Config{}, err
	}
	pc.APIKey = apiKey
	if apiKey == "" {
		printStepDone("API Key", "未设置")
	} else {
		printStepDone("API Key", maskKey(apiKey))
	}

	// Step 3: Base URL（直接回车使用官方默认地址）
	baseDefault := sp.baseURL
	baseHint := "留空使用官方地址"
	if baseDefault != "" {
		baseHint = baseDefault
	}
	baseURL, err := runTextInputWithDefault(r, "[3/4] Base URL（直接回车使用默认，代理用户填写代理地址）", baseHint, baseDefault)
	if err != nil {
		return Config{}, err
	}
	pc.BaseURL = baseURL
	if baseURL != "" {
		printStepDone("Base URL", baseURL)
	} else {
		printStepDone("Base URL", "默认")
	}

	// Step 4: 模型名（必填）
	modelName, err := runTextInput(r, "[4/4] 模型名称", "例如：gpt-4o / claude-sonnet-4 / gemini-2.5-pro")
	if err != nil {
		return Config{}, err
	}
	printStepDone("Model", modelName)

	cfg := Config{
		Provider:  providerName,
		ModelName: modelName,
		Providers: map[string]ProviderConfig{providerName: pc},
		Roles:     map[string]RoleConfig{},
		Style:     "default",
	}

	// 保存
	path := DefaultConfigPath()
	if err := SaveConfig(path, cfg); err != nil {
		return cfg, fmt.Errorf("save config: %w", err)
	}

	// 生成注释模板
	saveExampleConfig()

	// 全局偏好目录由启动流程（runWithConfig）统一创建，这里仅取路径用于提示
	rulesDir := rules.DefaultHomeRulesDir()

	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "✓ 配置已保存到 %s\n", path)
	fmt.Fprintf(os.Stderr, "  默认模型：%s\n", modelName)
	fmt.Fprintln(os.Stderr, "  如需按角色配置不同模型，编辑配置文件即可。")
	if rulesDir != "" {
		fmt.Fprintf(os.Stderr, "  全局写作偏好可放 %s 下的 .md 文件（见其中 README.txt）\n", rulesDir)
	}
	fmt.Fprintln(os.Stderr)

	return cfg, nil
}

func saveExampleConfig() {
	dir, err := configDir()
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, "config.example.jsonc"), []byte(exampleConfig), 0o644)
}

// printStepDone 打印一步完成的确认行。
func printStepDone(label, value string) {
	fmt.Fprintf(os.Stderr, "  ✓ %s: %s\n", label, value)
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
}

// ---------- stdin 交互 ----------

var apiTypeOptions = []setupProvider{
	{name: "openai", label: "OpenAI 兼容"},
	{name: "anthropic", label: "Anthropic 兼容"},
	{name: "gemini", label: "Gemini 兼容"},
}

// readLine 读取一行输入；EOF（管道结束/Ctrl-D）返回错误以中止引导。
func readLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("setup cancelled: %w", err)
	}
	return utils.CleanInputLine(line), nil
}

// runProviderSelect 打印编号列表，读取用户输入的序号并返回所选项。
func runProviderSelect(r *bufio.Reader, title string, items []setupProvider) (setupProvider, error) {
	for {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, title+"（输入序号后回车）:")
		for i, item := range items {
			fmt.Fprintf(os.Stderr, "  %2d) %s\n", i+1, item.label)
		}
		fmt.Fprint(os.Stderr, "❯ ")

		line, err := readLine(r)
		if err != nil {
			return setupProvider{}, err
		}
		n, convErr := strconv.Atoi(line)
		if convErr != nil || n < 1 || n > len(items) {
			fmt.Fprintf(os.Stderr, "  请输入 1-%d 之间的序号\n", len(items))
			continue
		}
		return items[n-1], nil
	}
}

// runTextInput 读取必填文本，留空则重新提示。
func runTextInput(r *bufio.Reader, label, placeholder string) (string, error) {
	for {
		fmt.Fprintf(os.Stderr, "\n%s\n", label)
		if placeholder != "" {
			fmt.Fprintf(os.Stderr, "  （%s）\n", placeholder)
		}
		fmt.Fprint(os.Stderr, "❯ ")
		line, err := readLine(r)
		if err != nil {
			return "", err
		}
		if line != "" {
			return line, nil
		}
		fmt.Fprintln(os.Stderr, "  该项必填，请输入内容")
	}
}

// runOptionalTextInput 读取可留空的文本。
func runOptionalTextInput(r *bufio.Reader, label string) (string, error) {
	fmt.Fprintf(os.Stderr, "\n%s\n❯ ", label)
	return readLine(r)
}

// runTextInputWithDefault 读取文本，留空时返回 defaultValue。
func runTextInputWithDefault(r *bufio.Reader, label, placeholder, defaultValue string) (string, error) {
	fmt.Fprintf(os.Stderr, "\n%s\n", label)
	if placeholder != "" {
		fmt.Fprintf(os.Stderr, "  （%s）\n", placeholder)
	}
	fmt.Fprint(os.Stderr, "❯ ")
	line, err := readLine(r)
	if err != nil {
		return "", err
	}
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}
