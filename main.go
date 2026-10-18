package main

import (
	"flag"
	"fmt"
	"os"
	"time"
)

const appVersion = "1.0.0"

func main() {
	var (
		cli        = flag.Bool("cli", false, "命令行模式（不启动 GUI）")
		configPath = flag.String("config", "", "网关 config.json 路径")
		baseURL    = flag.String("base-url", "", "网关地址，例如 http://127.0.0.1:7863")
		apiKey     = flag.String("api-key", "", "网关 API Key（可留空）")
		modelsPath = flag.String("models-file", "", "pi models.json 路径（默认 ~/.pi/agent/models.json）")
		provider   = flag.String("provider", "workbuddy", "pi provider 名称")
		dryRun     = flag.Bool("dry-run", false, "只预览变更，不写入文件")
		keepMiss   = flag.Bool("keep-missing", false, "保留未同步到的旧模型（默认删除）")
		noPanel    = flag.Bool("no-panel", false, "不读取 /panel/api/models 补充字段")
		only       = flag.String("only", "", "只同步指定模型 ID，逗号分隔")
		version    = flag.Bool("version", false, "显示版本")
	)
	flag.Parse()

	if *version {
		fmt.Printf("wb2pi-model-sync %s\n", appVersion)
		return
	}

	if !*cli {
		runGUI()
		return
	}

	logf := func(format string, args ...any) {
		fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
	}

	req := SyncRequest{
		ConfigPath:  *configPath,
		BaseURL:     *baseURL,
		APIKey:      *apiKey,
		ModelsPath:  *modelsPath,
		Provider:    *provider,
		UsePanel:    !*noPanel,
		KeepMissing: *keepMiss,
	}

	if *only != "" && !*keepMiss {
		logf("警告: --only 与默认删除策略同时使用，未列出的旧模型会被删除；只更新指定模型请加 --keep-missing。")
	}

	req, fetched, err := FetchAndMerge(req, logf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	desired, warnings, err := BuildDesired(fetched.Models, parseOnly(*only))
	for _, w := range warnings {
		logf("警告: %s", w)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}

	preview, err := BuildPreview(req, desired, logf)
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	fmt.Println()
	fmt.Print(preview.Plan.Render())

	if *dryRun {
		fmt.Println("\ndry-run：未写入任何文件。")
		return
	}
	if !preview.Plan.HasChanges() {
		fmt.Println("\n没有需要写入的变更。")
		return
	}
	if err := preview.Apply(logf); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
	logf("已写入: %s", preview.Plan.Path)
}
