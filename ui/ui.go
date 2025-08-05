package ui

import (
	"fmt"
	"go_proxy/proxy"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Apper 应用核心功能接口
// 定义了应用所需的所有核心功能，包括UI组件访问、代理管理和服务控制
// 所有UI事件处理函数都通过此接口与业务逻辑交互
type Apper interface {
	GetWindow() fyne.Window
	GetProxyList() binding.UntypedList
	GetLogBinding() binding.String
	GetProgressBar() *widget.ProgressBar
	GetServerStatus() binding.Bool
	Log(message string)
	FetchProxies()
	TestAllProxies()
	ImportProxies()
	ExportProxies()
	ClearProxies()
	ToggleServer(port string)
	ApplyFilters(maxLatency, minSpeed string)
}

// SetupUI 初始化应用主界面，排列所有UI组件
// 参数 app 提供了访问应用核心功能和数据绑定的接口
func SetupUI(app Apper) {
	toolbar := createToolbar(app)
	filterControl := createFilterControlPanel(app)
	serverControl := createServerControlPanel(app)
	progressCard := widget.NewCard("进度", "", app.GetProgressBar())

	proxyList := createProxyList(app)
	logView := createLogView(app)
	splitContent := container.NewHSplit(proxyList, logView)
	splitContent.SetOffset(0.7)

	topPanel := container.NewVBox(toolbar, filterControl, serverControl, progressCard)
	mainLayout := container.NewBorder(topPanel, nil, nil, nil, splitContent)

	win := app.GetWindow()
	win.SetContent(container.NewPadded(mainLayout))
	win.Resize(fyne.NewSize(1280, 800))
}

// createToolbar 创建顶部工具栏，包含代理操作的主要功能按钮
// 包括获取代理、测试代理、导入导出和清空列表等操作
func createToolbar(app Apper) fyne.CanvasObject {
	toolbar := widget.NewToolbar(
		widget.NewToolbarAction(theme.SearchIcon(), app.FetchProxies),
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(theme.ConfirmIcon(), app.TestAllProxies),
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(theme.FolderOpenIcon(), app.ImportProxies),
		widget.NewToolbarAction(theme.DownloadIcon(), app.ExportProxies),
		widget.NewToolbarSeparator(),
		widget.NewToolbarAction(theme.DeleteIcon(), func() {
			dialog.ShowConfirm("确认", "确定要清空所有代理列表吗?", func(ok bool) {
				if ok {
					app.ClearProxies()
				}
			}, app.GetWindow())
		}),
	)
	return container.NewPadded(toolbar)
}

// createFilterControlPanel 创建代理筛选控制面板
// 提供按延迟和速度筛选代理的功能，支持实时过滤代理列表
func createFilterControlPanel(app Apper) fyne.CanvasObject {
	latencyEntry := widget.NewEntry()
	latencyEntry.SetPlaceHolder("例如: 500 (ms)")

	speedEntry := widget.NewEntry()
	speedEntry.SetPlaceHolder("例如: 1 (Mbps)")

	applyBtn := widget.NewButtonWithIcon("应用筛选", theme.SearchReplaceIcon(), func() {
		app.ApplyFilters(latencyEntry.Text, speedEntry.Text)
	})

	grid := container.New(layout.NewFormLayout(),
		widget.NewLabel("最大延迟 (ms):"), latencyEntry,
		widget.NewLabel("最低速度 (Mbps):"), speedEntry,
	)

	accordion := widget.NewAccordion(
		widget.NewAccordionItem("筛选器", container.NewBorder(nil, nil, nil, applyBtn, grid)),
	)
	return accordion
}

// createServerControlPanel 创建本地代理服务控制面板
// 允许配置端口并启动/停止SOCKS5代理服务，显示当前服务状态
func createServerControlPanel(app Apper) *widget.Card {
	portEntry := widget.NewEntry()
	portEntry.SetPlaceHolder("例如: 10808")
	portEntry.SetText("10808")

	serverStatusBinding := app.GetServerStatus()
	statusLabel := widget.NewLabel("服务未运行")
	serverStatusBinding.AddListener(binding.NewDataListener(func() {
		running, _ := serverStatusBinding.Get()
		if running {
			statusLabel.SetText(fmt.Sprintf("服务运行于 127.0.0.1:%s", portEntry.Text))
		} else {
			statusLabel.SetText("服务未运行")
		}
	}))

	toggleServerBtn := widget.NewButtonWithIcon("启动服务", theme.MediaPlayIcon(), func() {
		app.ToggleServer(portEntry.Text)
	})
	serverStatusBinding.AddListener(binding.NewDataListener(func() {
		running, _ := serverStatusBinding.Get()
		if running {
			toggleServerBtn.SetText("停止服务")
			toggleServerBtn.SetIcon(theme.MediaStopIcon())
			portEntry.Disable()
		} else {
			toggleServerBtn.SetText("启动服务")
			toggleServerBtn.SetIcon(theme.MediaPlayIcon())
			portEntry.Enable()
		}
	}))

	grid := container.New(layout.NewFormLayout(),
		widget.NewLabel("本地SOCKS5端口:"), portEntry,
		widget.NewLabel("当前状态:"), statusLabel,
		layout.NewSpacer(), toggleServerBtn,
	)
	return widget.NewCard("服务控制", "启动本地代理服务以使用轮换IP", grid)
}

// createProxyList 创建代理列表表格视图
// 以表格形式展示所有可用代理，包含协议、地址、延迟、速度等关键信息
func createProxyList(app Apper) fyne.CanvasObject {
	data := app.GetProxyList()
	table := widget.NewTable(
		func() (int, int) { return data.Length() + 1, 6 },
		func() fyne.CanvasObject { return widget.NewLabel("Template") },
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				headers := []string{"协议", "代理地址", "延迟(ms)", "速度(Mbps)", "匿名度", "地区"}
				label.SetText(headers[id.Col])
				label.TextStyle.Bold = true
				return
			}
			item, err := data.GetValue(id.Row - 1)
			if err != nil {
				return
			}
			p := item.(*proxy.Proxy)
			var text string
			switch id.Col {
			case 0:
				text = p.Protocol
			case 1:
				text = p.Address
			case 2:
				if p.Latency > 0 {
					text = fmt.Sprintf("%.0f", p.Latency*1000)
				} else {
					text = "-"
				}
			case 3:
				if p.Speed > 0 {
					text = fmt.Sprintf("%.2f", p.Speed)
				} else {
					text = "-"
				}
			case 4:
				text = p.Anonymity
			case 5:
				text = p.Location
			}
			label.SetText(text)
			label.TextStyle.Bold = false
		},
	)
	table.SetColumnWidth(0, 60)
	table.SetColumnWidth(1, 180)
	table.SetColumnWidth(2, 80)
	table.SetColumnWidth(3, 90)
	table.SetColumnWidth(4, 90)
	table.SetColumnWidth(5, 60)
	return widget.NewCard("有效代理列表", "", table)
}

// createLogView 创建应用日志显示区域
// 实时显示应用操作日志和代理测试结果，支持自动滚动更新
func createLogView(app Apper) fyne.CanvasObject {
	logBinding := app.GetLogBinding()
	logEntry := widget.NewMultiLineEntry()
	logEntry.Bind(logBinding)
	logEntry.Disable()
	scroll := container.NewScroll(logEntry)
	logBinding.AddListener(binding.NewDataListener(func() {
		scroll.ScrollToBottom()
	}))
	return widget.NewCard("实时日志", "", scroll)
}
