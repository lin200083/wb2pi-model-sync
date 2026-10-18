package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/lxn/walk"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type appUI struct {
	mw     *walk.MainWindow
	closed atomic.Bool
	busy   bool

	configEdit *walk.LineEdit
	baseEdit   *walk.LineEdit
	keyEdit    *walk.LineEdit
	modelsEdit *walk.LineEdit

	fetchBtn   *walk.PushButton
	previewBtn *walk.PushButton
	syncBtn    *walk.PushButton
	selAllBtn  *walk.PushButton
	selNoneBtn *walk.PushButton
	clearBtn   *walk.PushButton

	keepMissing *walk.CheckBox
	usePanel    *walk.CheckBox

	table      *walk.TableView
	tableModel *modelTableModel
	logView    *walk.TextEdit
	status     *walk.Label
	selLabel   *walk.Label
	progress   *walk.ProgressBar
}

type modelRow struct {
	ID       string
	Name     string
	Context  int64
	MaxOut   int64
	Efforts  string
	Image    bool
	Tool     bool
	Promo    string
	Selected bool
	Model    Model
}

type modelTableModel struct {
	walk.TableModelBase
	rows    []*modelRow
	onCheck func()
}

func (m *modelTableModel) RowCount() int { return len(m.rows) }

func (m *modelTableModel) Value(row, col int) interface{} {
	if row < 0 || row >= len(m.rows) {
		return nil
	}
	r := m.rows[row]
	switch col {
	case 0:
		return r.ID
	case 1:
		return r.Name
	case 2:
		return formatTokens(r.Context)
	case 3:
		return formatTokens(r.MaxOut)
	case 4:
		return r.Efforts
	case 5:
		if r.Image {
			return "✓"
		}
		return ""
	case 6:
		if r.Tool {
			return "✓"
		}
		return ""
	case 7:
		if r.Promo != "" {
			return r.Promo
		}
		return "—"
	}
	return ""
}

func (m *modelTableModel) Checked(row int) bool {
	return row >= 0 && row < len(m.rows) && m.rows[row].Selected
}

func (m *modelTableModel) SetChecked(row int, checked bool) error {
	if row < 0 || row >= len(m.rows) {
		return nil
	}
	m.rows[row].Selected = checked
	if m.onCheck != nil {
		m.onCheck()
	}
	return nil
}

func (m *modelTableModel) Reset(rows []*modelRow) {
	m.rows = rows
	m.PublishRowsReset()
}

func formatTokens(n int64) string {
	if n <= 0 {
		return "—"
	}
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 10_000 {
		return fmt.Sprintf("%.0fK", float64(n)/1000)
	}
	if n >= 1000 {
		return fmt.Sprintf("%.1fK", float64(n)/1000)
	}
	return strconv.FormatInt(n, 10)
}

func runGUI() {
	var mw *walk.MainWindow
	defer func() {
		if r := recover(); r != nil {
			if mw != nil {
				walk.MsgBox(mw, "启动失败", fmt.Sprint(r), walk.MsgBoxIconError)
			} else {
				fmt.Fprintln(os.Stderr, "启动失败:", r)
			}
		}
	}()

	var err error
	mw, err = walk.NewMainWindow()
	if err != nil {
		fmt.Fprintln(os.Stderr, "创建窗口失败:", err)
		return
	}

	ui := &appUI{mw: mw}
	if err := ui.build(); err != nil {
		walk.MsgBox(mw, "构建界面失败", err.Error(), walk.MsgBoxIconError)
		return
	}

	mw.SetTitle("WorkBuddy → pi 模型同步")
	mw.SetSize(walk.Size{Width: 1000, Height: 720})
	_ = mw.SetMinMaxSize(walk.Size{Width: 900, Height: 640}, walk.Size{})
	mw.Closing().Attach(func(canceled *bool, reason walk.CloseReason) {
		ui.closed.Store(true)
	})

	ui.autoFill()
	ui.refreshButtons()
	mw.Show()
	ui.progress.SetVisible(false)
	mw.Run()
}

func (ui *appUI) build() error {
	mainLayout := walk.NewVBoxLayout()
	_ = mainLayout.SetMargins(walk.Margins{HNear: 10, VNear: 10, HFar: 10, VFar: 10})
	_ = mainLayout.SetSpacing(6)
	if err := ui.mw.SetLayout(mainLayout); err != nil {
		return err
	}

	desc := must(walk.NewLabel(ui.mw))
	_ = desc.SetText("从 WorkBuddy2API 网关获取模型，写入 pi 的 models.json。cn:auto 始终排除；同名模型覆盖管理字段。")

	// Row 1: gateway config.json
	row1, l1 := ui.newRow()
	formLabel(row1, "网关配置")
	ui.configEdit = addLineEdit(row1, l1, false)
	addButton(row1, "浏览…", func() { ui.browseConfig() })
	addButton(row1, "读取", func() { ui.readConfig() })

	// Row 2: gateway base URL
	row2, l2 := ui.newRow()
	formLabel(row2, "网关地址")
	ui.baseEdit = addLineEdit(row2, l2, false)
	hint := must(walk.NewLabel(row2))
	_ = hint.SetText("例如 http://127.0.0.1:7863")

	// Row 3: API key
	row3, l3 := ui.newRow()
	formLabel(row3, "API Key")
	ui.keyEdit = addLineEdit(row3, l3, true)
	showKey := must(walk.NewCheckBox(row3))
	_ = showKey.SetText("显示")
	showKey.CheckedChanged().Attach(func() {
		ui.keyEdit.SetPasswordMode(!showKey.Checked())
	})

	// Row 4: pi models.json
	row4, l4 := ui.newRow()
	formLabel(row4, "pi 配置")
	ui.modelsEdit = addLineEdit(row4, l4, false)
	addButton(row4, "浏览…", func() { ui.browseModels() })
	addButton(row4, "打开目录", func() { ui.openModelsDir() })

	// Row 5: actions
	row5, l5 := ui.newRow()
	ui.fetchBtn = addButton(row5, "获取模型", func() { ui.onFetch() })
	ui.previewBtn = addButton(row5, "预览变更", func() { ui.onPreview() })
	ui.syncBtn = addButton(row5, "开始同步", func() { ui.onSync() })
	ui.selAllBtn = addButton(row5, "全选", func() { ui.setAll(true) })
	ui.selNoneBtn = addButton(row5, "全不选", func() { ui.setAll(false) })
	ui.clearBtn = addButton(row5, "清空日志", func() { ui.clearLog() })
	ui.selLabel = must(walk.NewLabel(row5))
	_ = ui.selLabel.SetText("尚未获取模型")
	_ = l5.SetStretchFactor(ui.selLabel, 20)

	// Row 6: options
	row6, _ := ui.newRow()
	ui.keepMissing = must(walk.NewCheckBox(row6))
	_ = ui.keepMissing.SetText("删除未同步到的旧模型（含未勾选）")
	ui.keepMissing.SetChecked(true)
	ui.usePanel = must(walk.NewCheckBox(row6))
	_ = ui.usePanel.SetText("读取 /panel/api/models 补充思考档位等字段")
	ui.usePanel.SetChecked(true)
	note := must(walk.NewLabel(row6))
	_ = note.SetText("内容无变化不写入；不修改 settings.json")

	// Table
	ui.table = must(walk.NewTableView(ui.mw))
	ui.table.SetAlternatingRowBG(true)
	ui.table.SetLastColumnStretched(true)
	ui.table.SetColumnsSizable(true)
	columns := []struct {
		title string
		width int
		align walk.Alignment1D
	}{
		{"模型 ID", 190, walk.AlignNear},
		{"名称（含倍率）", 240, walk.AlignNear},
		{"上下文", 66, walk.AlignFar},
		{"最大输出", 76, walk.AlignFar},
		{"思考档位", 160, walk.AlignNear},
		{"图片", 42, walk.AlignCenter},
		{"工具", 42, walk.AlignCenter},
		{"促销", 84, walk.AlignNear},
	}
	for _, c := range columns {
		col := walk.NewTableViewColumn()
		_ = col.SetTitle(c.title)
		_ = col.SetWidth(c.width)
		_ = col.SetAlignment(c.align)
		_ = ui.table.Columns().Add(col)
	}
	ui.tableModel = &modelTableModel{onCheck: ui.updateSelectionStatus}
	if err := ui.table.SetModel(ui.tableModel); err != nil {
		return err
	}
	ui.table.SetCheckBoxes(true)
	_ = mainLayout.SetStretchFactor(ui.table, 1)

	// Log
	logHeader, _ := ui.newRow()
	formLabel(logHeader, "运行日志")
	logHint := must(walk.NewLabel(logHeader))
	_ = logHint.SetText("请求、变更预览与错误信息")
	ui.logView = must(walk.NewTextEdit(ui.mw))
	_ = ui.logView.SetReadOnly(true)
	ui.logView.SetCompactHeight(false)
	if font, err := walk.NewFont("Consolas", 9, 0); err == nil {
		ui.logView.SetFont(font)
	}
	_ = ui.logView.SetMinMaxSize(walk.Size{Height: 100}, walk.Size{Height: 150})

	// Status
	statusRow, statusLayout := ui.newRow()
	ui.progress = must(walk.NewProgressBar(statusRow))
	_ = ui.progress.SetMarqueeMode(true)
	ui.progress.SetVisible(false)
	ui.status = must(walk.NewLabel(statusRow))
	_ = ui.status.SetText("就绪")
	_ = statusLayout.SetStretchFactor(ui.status, 20)

	ui.logf("界面已启动。")
	return nil
}

func (ui *appUI) newRow() (*walk.Composite, *walk.BoxLayout) {
	c := must(walk.NewComposite(ui.mw))
	l := walk.NewHBoxLayout()
	_ = l.SetMargins(walk.Margins{})
	_ = c.SetLayout(l)
	return c, l
}

func formLabel(parent walk.Container, text string) *walk.Label {
	l := must(walk.NewLabel(parent))
	_ = l.SetText(text)
	_ = l.SetMinMaxSize(walk.Size{Width: 84}, walk.Size{Width: 84})
	return l
}

func addLineEdit(parent walk.Container, layout *walk.BoxLayout, password bool) *walk.LineEdit {
	le := must(walk.NewLineEdit(parent))
	if password {
		le.SetPasswordMode(true)
	}
	_ = layout.SetStretchFactor(le, 20)
	return le
}

func addButton(parent walk.Container, text string, onClick func()) *walk.PushButton {
	b := must(walk.NewPushButton(parent))
	_ = b.SetText(text)
	b.Clicked().Attach(onClick)
	return b
}

func (ui *appUI) autoFill() {
	_ = ui.modelsEdit.SetText(DefaultModelsPath())
	_ = ui.baseEdit.SetText("http://127.0.0.1:7863")

	if p := FindGatewayConfig(); p != "" {
		_ = ui.configEdit.SetText(p)
		if cfg, err := ReadGatewayConfig(p); err == nil {
			_ = ui.baseEdit.SetText(GatewayRootFromListen(cfg.Listen))
			_ = ui.keyEdit.SetText(cfg.APIKey)
			ui.logf("已自动读取网关配置: %s", p)
			ui.logf("网关地址: %s，API Key: %s", GatewayRootFromListen(cfg.Listen), maskKey(cfg.APIKey))
		} else {
			ui.logf("读取自动发现的网关配置失败: %v", err)
		}
	} else {
		ui.logf("未自动发现网关 config.json，请手动填写地址和 API Key。")
	}
	ui.logf("pi 配置: %s", ui.modelsEdit.Text())
	ui.logf("勾选模型后点击“预览变更”或“开始同步”。")
}

func (ui *appUI) request() SyncRequest {
	return SyncRequest{
		ConfigPath:  ui.configEdit.Text(),
		BaseURL:     ui.baseEdit.Text(),
		APIKey:      ui.keyEdit.Text(),
		ModelsPath:  ui.modelsEdit.Text(),
		Provider:    "workbuddy",
		UsePanel:    ui.usePanel.Checked(),
		KeepMissing: !ui.keepMissing.Checked(),
	}
}

func (ui *appUI) browseConfig() {
	path := ui.pickFile("选择网关 config.json", "JSON 文件 (*.json)|*.json|所有文件 (*.*)|*.*", ui.configEdit.Text())
	if path == "" {
		return
	}
	_ = ui.configEdit.SetText(path)
	ui.readConfig()
}

func (ui *appUI) browseModels() {
	path := ui.pickFile("选择 pi models.json", "JSON 文件 (*.json)|*.json|所有文件 (*.*)|*.*", ui.modelsEdit.Text())
	if path != "" {
		_ = ui.modelsEdit.SetText(path)
	}
}

func (ui *appUI) pickFile(title, filter, current string) string {
	dlg := new(walk.FileDialog)
	dlg.Title = title
	dlg.Filter = filter
	if strings.TrimSpace(current) != "" {
		dlg.InitialDirPath = filepath.Dir(current)
	}
	accepted, err := dlg.ShowOpen(ui.mw)
	if err != nil {
		walk.MsgBox(ui.mw, "选择文件失败", err.Error(), walk.MsgBoxIconError)
		return ""
	}
	if !accepted {
		return ""
	}
	return dlg.FilePath
}

func (ui *appUI) readConfig() {
	path := strings.TrimSpace(ui.configEdit.Text())
	if path == "" {
		walk.MsgBox(ui.mw, "提示", "请先选择网关 config.json。", walk.MsgBoxIconInformation)
		return
	}
	cfg, err := ReadGatewayConfig(path)
	if err != nil {
		ui.logf("读取网关配置失败: %v", err)
		walk.MsgBox(ui.mw, "读取失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	_ = ui.baseEdit.SetText(GatewayRootFromListen(cfg.Listen))
	_ = ui.keyEdit.SetText(cfg.APIKey)
	ui.logf("已读取网关配置: listen=%s，API Key: %s", cfg.Listen, maskKey(cfg.APIKey))
}

func (ui *appUI) openModelsDir() {
	path := strings.TrimSpace(ui.modelsEdit.Text())
	if path == "" {
		return
	}
	if _, err := os.Stat(path); err == nil {
		_ = exec.Command("explorer.exe", "/select,"+path).Start()
		return
	}
	dir := filepath.Dir(path)
	_ = os.MkdirAll(dir, 0o700)
	_ = exec.Command("explorer.exe", dir).Start()
}

func (ui *appUI) onFetch() {
	if ui.busy {
		return
	}
	req := ui.request()
	ui.setBusy(true)
	ui.status.SetText("正在获取模型…")
	ui.logf("开始获取模型…")

	go func() {
		_, fetched, err := FetchAndMerge(req, ui.logAsync)
		if ui.closed.Load() {
			return
		}
		ui.mw.Synchronize(func() {
			ui.setBusy(false)
			if err != nil {
				ui.status.SetText("获取失败")
				ui.logf("获取失败: %v", err)
				walk.MsgBox(ui.mw, "获取模型失败", err.Error(), walk.MsgBoxIconError)
				return
			}
			ui.populate(fetched)
			ui.status.SetText(fmt.Sprintf("已获取 %d 个模型（已排除 cn:auto）", len(fetched.Models)))
			ui.logf("已填充 %d 个模型，默认全选。", len(fetched.Models))
			if !fetched.PanelOK {
				ui.logf("提示: /panel/api/models 不可用，思考档位等字段来自 /v1/models。")
			}
		})
	}()
}

func (ui *appUI) populate(fetched *FetchResult) {
	rows := make([]*modelRow, 0, len(fetched.Models))
	for i := range fetched.Models {
		m := fetched.Models[i]
		rows = append(rows, &modelRow{
			ID:       m.ID,
			Name:     m.DisplayName(),
			Context:  m.ContextLength,
			MaxOut:   m.MaxOutputTokens,
			Efforts:  FormatEfforts(BuildThinkingLevelMap(&m)),
			Image:    m.SupportsImages,
			Tool:     m.SupportsToolCall,
			Promo:    m.PromoLabel,
			Selected: true,
			Model:    m,
		})
	}
	ui.tableModel.Reset(rows)
	ui.table.SetCheckBoxes(true)
	ui.updateSelectionStatus()
	ui.refreshButtons()
}

func (ui *appUI) desiredFromRows() ([]map[string]any, error) {
	desired := make([]map[string]any, 0, len(ui.tableModel.rows))
	for _, r := range ui.tableModel.rows {
		if !r.Selected {
			continue
		}
		entry, err := ToPiModel(r.Model)
		if err != nil {
			ui.logf("跳过 %s: %v", r.ID, err)
			continue
		}
		desired = append(desired, entry)
	}
	if len(desired) == 0 {
		return nil, fmt.Errorf("没有勾选任何模型")
	}
	return desired, nil
}

func (ui *appUI) onPreview() {
	if ui.busy {
		return
	}
	desired, err := ui.desiredFromRows()
	if err != nil {
		walk.MsgBox(ui.mw, "提示", err.Error(), walk.MsgBoxIconInformation)
		return
	}
	preview, err := BuildPreview(ui.request(), desired, ui.logf)
	if err != nil {
		ui.logf("预览失败: %v", err)
		walk.MsgBox(ui.mw, "预览失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	ui.logf("--- 变更预览 ---")
	ui.logBlock(preview.Plan.Render())
	ui.status.SetText(preview.Plan.Summary())
}

func (ui *appUI) onSync() {
	if ui.busy {
		return
	}
	desired, err := ui.desiredFromRows()
	if err != nil {
		walk.MsgBox(ui.mw, "提示", err.Error(), walk.MsgBoxIconInformation)
		return
	}
	preview, err := BuildPreview(ui.request(), desired, ui.logf)
	if err != nil {
		ui.logf("同步准备失败: %v", err)
		walk.MsgBox(ui.mw, "同步失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	if !preview.Plan.HasChanges() {
		ui.logf("没有需要写入的变更。")
		ui.status.SetText(preview.Plan.Summary())
		walk.MsgBox(ui.mw, "无需同步", "配置已经是最新，没有变更。", walk.MsgBoxIconInformation)
		return
	}

	deleteOld := "是"
	if ui.keepMissing.Checked() {
		deleteOld = "否（保留未同步的旧模型）"
	}
	msg := fmt.Sprintf(
		"将写入：\n%s\n\n%s\n\n删除未同步到的旧模型：%s\n\n是否继续？",
		preview.Plan.Path, preview.Plan.Summary(), deleteOld,
	)
	if walk.MsgBox(ui.mw, "确认同步", msg, walk.MsgBoxYesNo|walk.MsgBoxIconQuestion) != walk.DlgCmdYes {
		ui.logf("已取消同步。")
		return
	}

	ui.setBusy(true)
	ui.status.SetText("正在写入…")
	ui.logf("--- 开始同步 ---")
	ui.logBlock(preview.Plan.Render())

	if err := preview.Apply(ui.logf); err != nil {
		ui.setBusy(false)
		ui.status.SetText("同步失败")
		ui.logf("同步失败: %v", err)
		walk.MsgBox(ui.mw, "同步失败", err.Error(), walk.MsgBoxIconError)
		return
	}
	ui.setBusy(false)

	ui.status.SetText("同步完成：" + preview.Plan.Summary())
	ui.logf("同步完成: %s", preview.Plan.Summary())
	walk.MsgBox(ui.mw, "同步完成",
		fmt.Sprintf("%s\n\n配置文件：\n%s", preview.Plan.Summary(), preview.Plan.Path),
		walk.MsgBoxIconInformation)
}

func (ui *appUI) setAll(checked bool) {
	for _, r := range ui.tableModel.rows {
		r.Selected = checked
	}
	_ = ui.table.Invalidate()
	ui.updateSelectionStatus()
}

func (ui *appUI) updateSelectionStatus() {
	total := len(ui.tableModel.rows)
	selected := 0
	for _, r := range ui.tableModel.rows {
		if r.Selected {
			selected++
		}
	}
	if total == 0 {
		_ = ui.selLabel.SetText("尚未获取模型")
		return
	}
	_ = ui.selLabel.SetText(fmt.Sprintf("已选 %d / %d", selected, total))
}

func (ui *appUI) setBusy(busy bool) {
	ui.busy = busy
	if busy {
		_ = ui.progress.SetMarqueeMode(true)
	}
	ui.progress.SetVisible(busy)
	ui.refreshButtons()
}

func (ui *appUI) refreshButtons() {
	hasRows := len(ui.tableModel.rows) > 0
	ui.fetchBtn.SetEnabled(!ui.busy)
	ui.previewBtn.SetEnabled(!ui.busy && hasRows)
	ui.syncBtn.SetEnabled(!ui.busy && hasRows)
	ui.selAllBtn.SetEnabled(!ui.busy && hasRows)
	ui.selNoneBtn.SetEnabled(!ui.busy && hasRows)
	ui.progress.SetVisible(ui.busy)
}

func (ui *appUI) clearLog() {
	_ = ui.logView.SetText("")
	ui.logf("日志已清空。")
}

func (ui *appUI) logf(format string, args ...any) {
	ui.logLine(fmt.Sprintf(format, args...))
}

func (ui *appUI) logAsync(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if ui.closed.Load() {
		return
	}
	ui.mw.Synchronize(func() {
		if !ui.closed.Load() {
			ui.logLine(msg)
		}
	})
}

func (ui *appUI) logLine(msg string) {
	if ui.logView == nil {
		return
	}
	ui.logView.AppendText(fmt.Sprintf("[%s] %s\r\n", time.Now().Format("15:04:05"), msg))
	n := ui.logView.TextLength()
	ui.logView.SetTextSelection(n, n)
	ui.logView.ScrollToCaret()
}

func (ui *appUI) logBlock(text string) {
	for _, line := range strings.Split(strings.TrimRight(text, "\r\n"), "\n") {
		ui.logLine(strings.TrimRight(line, "\r"))
	}
}

func maskKey(key string) string {
	if key == "" {
		return "(空)"
	}
	if len(key) <= 8 {
		return "********"
	}
	return key[:4] + "…" + key[len(key)-2:]
}
