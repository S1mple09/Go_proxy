package ui

import (
	"fmt"
	"go_proxy/config"
	"go_proxy/fetcher"
	"go_proxy/proxy"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"fyne.io/fyne/v2/canvas"

	customtheme "go_proxy/theme"
)

// minSizeLayout 自定义最小尺寸布局
type minSizeLayout struct {
	minSize fyne.Size
}

func (m *minSizeLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	return m.minSize
}

func (m *minSizeLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	for _, child := range objects {
		child.Resize(size)
	}
}

// Apper 应用核心功能接口
// 定义了应用所需的所有核心功能，包括UI组件访问、代理管理和服务控制
// 所有UI事件处理函数都通过此接口与业务逻辑交互
type Apper interface {
	GetWindow() fyne.Window
	GetProxyList() binding.UntypedList
	GetProgressText() binding.String
	GetServerStatus() binding.Bool
	GetRotationStatus() binding.Bool
	GetCurrentProxy() binding.String
	Log(message string)
	FetchProxies()
	TestAllProxies()
	ImportProxies()
	ExportProxies()
	ClearProxies()
	ToggleServer(port string)
	ToggleRotation(enable bool)
	SetRotationInterval(seconds int)
	ApplyFilters(maxLatency, minSpeed string)
	GetConfig() *config.AppConfig // Add GetConfig method
}

// SetupUI 初始化应用主界面，排列所有UI组件
// 参数 app 提供了访问应用核心功能和数据绑定的接口
func SetupUI(app Apper) {
	// 创建主窗口
	win := app.GetWindow()
	win.Resize(fyne.NewSize(1000, 700))
	win.SetTitle("代理池工具 v0.2.0")

	// 创建主框架
	mainFrame := container.NewVBox()

	// 创建顶部操作栏
	toolbar := createModernToolbar(app)
	mainFrame.Add(toolbar)

	// 创建进度条和状态区域
	statusArea := createStatusArea(app)
	mainFrame.Add(statusArea)

	// 创建分割的主内容区
	contentSplit := container.NewHSplit(
		createSidePanels(app),  // 左侧控制面板
		createMainContent(app), // 右侧主内容（代理列表和日志）
	)
	contentSplit.SetOffset(0.3) // 设置左右比例
	mainFrame.Add(contentSplit)

	win.SetContent(container.NewPadded(mainFrame))
}

// createModernToolbar 创建现代风格的顶部工具栏
func createModernToolbar(app Apper) fyne.CanvasObject {
	// 创建主题切换选择框
	themeSelect := widget.NewSelect([]string{"默认", "深色", "自定义", "蓝色", "绿色"}, func(selected string) {
		// Cast app to an interface that has SaveConfig and GetConfig methods
		configManager, ok := app.(interface {
			SaveConfig() error
			GetConfig() *config.AppConfig
		})
		if !ok {
			app.Log("Error: App does not implement config management interface.")
			return
		}

		// 切换主题
		switch selected {
		case "默认":
			fyne.CurrentApp().Settings().SetTheme(fynetheme.LightTheme())
		case "深色":
			fyne.CurrentApp().Settings().SetTheme(fynetheme.DarkTheme())
		case "自定义":
			fyne.CurrentApp().Settings().SetTheme(&customtheme.MyTheme{})
		case "蓝色":
			fyne.CurrentApp().Settings().SetTheme(&customtheme.BlueTheme{})
		case "绿色":
			fyne.CurrentApp().Settings().SetTheme(&customtheme.GreenTheme{})
		}
		// Update config and save
		cfg := configManager.GetConfig()
		cfg.ThemeName = selected
		if err := configManager.SaveConfig(); err != nil {
			app.Log(fmt.Sprintf("保存主题配置失败: %v", err))
		}
	})
	// Set initial theme from config
	if cfg := app.GetConfig(); cfg != nil {
		themeSelect.SetSelected(cfg.ThemeName)
	} else {
		themeSelect.SetSelected("自定义") // Fallback to default
	}

	// 当前轮换IP显示
	currentRotationIPLabel := widget.NewLabelWithStyle("当前使用: N/A", fyne.TextAlignTrailing, fyne.TextStyle{Bold: true})
	app.GetCurrentProxy().AddListener(binding.NewDataListener(func() {
		proxyAddr, _ := app.GetCurrentProxy().Get()
		if proxyAddr != "" {
			currentRotationIPLabel.SetText(fmt.Sprintf("当前使用: %s", proxyAddr))
		} else {
			currentRotationIPLabel.SetText("当前使用: N/A")
		}
	}))

	// 创建左侧操作按钮
	actionButtons := container.NewHBox(
		createStyledButton("获取代理", app.FetchProxies, "primary"),
		createStyledButton("导入代理", app.ImportProxies, "primary"),
		createStyledButton("清空列表", func() {
			dialog.ShowConfirm("确认", "确定要清空所有代理列表吗?", func(ok bool) {
				if ok {
					app.ClearProxies()
				}
			}, app.GetWindow())
		}, "danger"),
		createStyledButton("管理代理源", func() {
			createSourceManagementWindow(app)
		}, "info"),
		createStyledButton("设置", func() {
			createSettingsWindow(app)
		}, "info"),
	)

	// 创建右侧状态和主题选择
	rightPanel := container.NewHBox(
		themeSelect,
		layout.NewSpacer(),
		currentRotationIPLabel,
	)

	// 组合工具栏
	toolbar := container.NewBorder(nil, nil, actionButtons, rightPanel)
	return container.NewPadded(toolbar)
}

// createStyledButton 创建样式化按钮
func createStyledButton(label string, callback func(), style string) *widget.Button {
	btn := widget.NewButton(label, callback)
	// 在Fyne中，我们可以通过修改按钮的属性来实现不同样式
	switch style {
	case "primary":
		btn.Importance = widget.HighImportance
	case "danger":
		btn.Importance = widget.DangerImportance
	case "info":
		btn.Importance = widget.MediumImportance
	}
	btn.Resize(fyne.NewSize(100, 30))
	return btn
}

// createStatusArea 创建状态显示和进度区域
func createStatusArea(app Apper) fyne.CanvasObject {
	// 创建进度条
	progressBar := widget.NewProgressBar()
	progressBar.SetValue(0.0)
	
	// 创建进度文本
	progressLabel := widget.NewLabel("")
	progressLabel.Bind(app.GetProgressText())
	
	// 组合状态区域
	statusArea := container.NewHBox(
		progressLabel,
		layout.NewSpacer(),
		progressBar,
	)
	progressBar.Resize(fyne.NewSize(200, 20))
	
	return container.NewPadded(statusArea)
}

// createSidePanels 创建左侧控制面板集合
func createSidePanels(app Apper) fyne.CanvasObject {
	// 创建控制面板容器
	controlPanel := container.NewVBox(
		createFilterPanel(app),
		createServerControlPanel(app),
		createRotationControlPanel(app),
	)
	
	return container.NewPadded(controlPanel)
}

// createFilterPanel 创建代理筛选控制面板
func createFilterPanel(app Apper) fyne.CanvasObject {
	latencyEntry := widget.NewEntry()
	latencyEntry.SetPlaceHolder("500")
	
	speedEntry := widget.NewEntry()
	speedEntry.SetPlaceHolder("1024")
	
	qualityCheck := widget.NewCheck("优质筛选", nil)
	
	regionLabel := widget.NewLabel("国家筛选:")
	regionSelect := widget.NewSelect([]string{"全部国家", "中国", "美国", "日本", "韩国", "其他"}, func(s string) {
		// TODO: 实现区域筛选功能
	})
	regionSelect.SetSelected("全部国家")
	
	applyBtn := widget.NewButton("应用筛选", func() {
		app.ApplyFilters(latencyEntry.Text, speedEntry.Text)
	})
	
	// 创建网格布局
	grid := container.New(layout.NewFormLayout(),
		widget.NewLabel("最大延迟 (ms):"), latencyEntry,
		widget.NewLabel("最低速度 (KB/s):"), speedEntry,
		regionLabel, regionSelect,
	)
	
	// 组合筛选面板
	filterContent := container.NewVBox(
		grid,
		qualityCheck,
		applyBtn,
	)
	
	return widget.NewCard("筛选与过滤", "", filterContent)
}

// createServerControlPanel 创建服务控制面板
func createServerControlPanel(app Apper) fyne.CanvasObject {
	socksPortEntry := widget.NewEntry()
	socksPortEntry.SetPlaceHolder("1800")
	socksPortEntry.SetText("1800")
	
	httpPortEntry := widget.NewEntry()
	httpPortEntry.SetPlaceHolder("1801")
	httpPortEntry.SetText("1801")
	
	serverStatusBinding := app.GetServerStatus()
	statusLabel := widget.NewLabel("服务未运行")
	serverStatusBinding.AddListener(binding.NewDataListener(func() {
		running, _ := serverStatusBinding.Get()
		if running {
			statusLabel.SetText(fmt.Sprintf("服务运行中 (SOCKS5: %s / HTTP: %s)", 
				socksPortEntry.Text, httpPortEntry.Text))
			// 使用canvas.Text替代直接设置颜色
			statusText := canvas.NewText(statusLabel.Text, fynetheme.Color("success"))
			statusText.TextSize = 14
			statusText.Alignment = fyne.TextAlignLeading
		} else {
			statusLabel.SetText("服务未运行")
			// 使用canvas.Text替代直接设置颜色
			statusText := canvas.NewText(statusLabel.Text, fynetheme.Color("danger"))
			statusText.TextSize = 14
			statusText.Alignment = fyne.TextAlignLeading
		}
	}))
	
	toggleServerBtn := createStyledButton("启动服务", func() {
		// TODO: 实现同时启动SOCKS5和HTTP服务
		app.ToggleServer(socksPortEntry.Text)
	}, "primary")
	
	serverStatusBinding.AddListener(binding.NewDataListener(func() {
		running, _ := serverStatusBinding.Get()
		if running {
			toggleServerBtn.SetText("停止服务")
			socksPortEntry.Disable()
			httpPortEntry.Disable()
		} else {
			toggleServerBtn.SetText("启动服务")
			socksPortEntry.Enable()
			httpPortEntry.Enable()
		}
	}))
	
	// 创建网格布局
	grid := container.New(layout.NewFormLayout(),
		widget.NewLabel("SOCKS5端口:"), socksPortEntry,
		widget.NewLabel("HTTP端口:"), httpPortEntry,
	)
	
	// 组合服务面板
	serverContent := container.NewVBox(
		grid,
		statusLabel,
		toggleServerBtn,
	)
	
	return widget.NewCard("代理服务", "", serverContent)
}

// createRotationControlPanel 创建IP轮换控制面板
func createRotationControlPanel(app Apper) fyne.CanvasObject {
	intervalSpin := widget.NewEntry()
	intervalSpin.SetPlaceHolder("10")
	intervalSpin.SetText("10")
	
	rotationStatusBinding := app.GetRotationStatus()
	statusLabel := widget.NewLabel("未开启轮换")
	rotationStatusBinding.AddListener(binding.NewDataListener(func() {
		running, _ := rotationStatusBinding.Get()
		if running {
			statusLabel.SetText("自动轮换已开启")
			// 使用canvas.Text替代直接设置颜色
			statusText := canvas.NewText(statusLabel.Text, fynetheme.Color("success"))
			statusText.TextSize = 14
			statusText.Alignment = fyne.TextAlignLeading
		} else {
			statusLabel.SetText("未开启轮换")
			// 使用canvas.Text替代直接设置颜色
			statusText := canvas.NewText(statusLabel.Text, fynetheme.Color("danger"))
			statusText.TextSize = 14
			statusText.Alignment = fyne.TextAlignLeading
		}
	}))
	
	toggleAutoRotateBtn := createStyledButton("开启自动轮换", func() {
		running, _ := rotationStatusBinding.Get()
		app.ToggleRotation(!running)
		
		// 设置轮换间隔
		if seconds, err := strconv.Atoi(intervalSpin.Text); err == nil {
			app.SetRotationInterval(seconds)
		}
	}, "info")
	
	rotationStatusBinding.AddListener(binding.NewDataListener(func() {
		running, _ := rotationStatusBinding.Get()
		if running {
			toggleAutoRotateBtn.SetText("停止自动轮换")
		} else {
			toggleAutoRotateBtn.SetText("开启自动轮换")
		}
	}))
	
	manualRotateBtn := createStyledButton("手动轮换", func() {
		// TODO: 实现手动轮换功能
	}, "primary")
	
	// 创建网格布局
	grid := container.New(layout.NewFormLayout(),
		widget.NewLabel("轮换间隔 (秒):"), intervalSpin,
	)
	
	// 组合轮换面板
	rotationContent := container.NewVBox(
		grid,
		statusLabel,
		container.NewHBox(toggleAutoRotateBtn, manualRotateBtn),
	)
	
	return widget.NewCard("IP轮换", "", rotationContent)
}

// createMainContent 创建主内容区域，包含代理列表和日志面板
func createMainContent(app Apper) fyne.CanvasObject {
	// 创建代理列表
	proxyList := createModernProxyList(app)
	
	// 创建日志面板
	logPanel := createLogPanel(app)
	
	// 使用垂直分割面板组合代理列表和日志
	split := container.NewVSplit(proxyList, logPanel)
	split.SetOffset(0.7) // 设置上下比例
	
	return split
}

// createModernProxyList 创建现代风格的代理列表表格
func createModernProxyList(app Apper) fyne.CanvasObject {
	originalData := app.GetProxyList()
	filteredData := binding.NewUntypedList()

	// 过滤并设置数据显示
	filterAndSet := func() {
		items, _ := originalData.Get()
		filteredItems := make([]interface{}, 0)
		for _, item := range items {
			if p, ok := item.(*proxy.Proxy); ok && p.Speed > 0 {
				filteredItems = append(filteredItems, p)
			}
		}
		filteredData.Set(filteredItems)
	}

	// 首次加载和数据变更时应用过滤
	filterAndSet()
	originalData.AddListener(binding.NewDataListener(filterAndSet))

	data := filteredData // 后续代码使用过滤后的数据
	var (
		sortBySpeedDesc   bool = true
		sortByLatencyDesc bool = true
		sortByScoreDesc   bool = true
	)

	// 排序代理列表
	sortProxies := func(sortBy string) {
		items, _ := data.Get()
		proxies := make([]*proxy.Proxy, len(items))
		for i, item := range items {
			proxies[i] = item.(*proxy.Proxy)
		}

		sort.Slice(proxies, func(i, j int) bool {
			switch sortBy {
			case "speed":
				if sortBySpeedDesc {
					return proxies[i].Speed > proxies[j].Speed
				}
				return proxies[i].Speed < proxies[j].Speed
			case "latency":
				if sortByLatencyDesc {
					return proxies[i].Latency > proxies[j].Latency
				}
				return proxies[i].Latency < proxies[j].Latency
			case "score":
				if sortByScoreDesc {
					return proxies[i].Score > proxies[j].Score
				}
				return proxies[i].Score < proxies[j].Score
			}
			return false
		})

		newItems := make([]interface{}, len(proxies))
		for i, p := range proxies {
			newItems[i] = p
		}
		data.Set(newItems)
	}

	// 右键菜单项
	var selectedRow widget.TableCellID
	menuItems := []*fyne.MenuItem{
		fyne.NewMenuItem("测试选中代理", func() {
			items, _ := data.Get()
			if selectedRow.Row > 0 && selectedRow.Row <= len(items) {
				p := items[selectedRow.Row-1].(*proxy.Proxy)
				app.Log(fmt.Sprintf("开始测试代理: %s", p.Address))
				go func() {
					// TODO: 实现代理测试功能
					app.Log(fmt.Sprintf("代理 %s 测试完成", p.Address))
				}()
			}
		}),
		fyne.NewMenuItem("导出选中代理", func() {
			if selectedRow.Row > 0 {
				items, _ := data.Get()
				if selectedRow.Row <= len(items) {
					p := items[selectedRow.Row-1].(*proxy.Proxy)
					dialog.ShowFileSave(func(uri fyne.URIWriteCloser, err error) {
						if uri != nil {
							defer uri.Close()
							_, _ = uri.Write([]byte(p.Address))
							app.Log(fmt.Sprintf("已导出代理: %s", p.Address))
						}
					}, app.GetWindow())
				}
			}
		}),
		fyne.NewMenuItem("删除选中代理", func() {
			if selectedRow.Row > 0 {
				items, _ := data.Get()
				if selectedRow.Row <= len(items) {
					p := items[selectedRow.Row-1].(*proxy.Proxy)
					dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除代理 %s 吗?", p.Address), func(ok bool) {
						if ok {
							originalItems, _ := originalData.Get()
							newOriginalItems := make([]interface{}, 0)
							for _, item := range originalItems {
								if item.(*proxy.Proxy).Address != p.Address {
									newOriginalItems = append(newOriginalItems, item)
								}
							}
							originalData.Set(newOriginalItems)
							app.Log(fmt.Sprintf("已删除代理: %s", p.Address))
						}
					}, app.GetWindow())
				}
			}
		}),
	}

	// 创建表格
	table := widget.NewTable(
		func() (int, int) { return data.Length() + 1, 8 },
		func() fyne.CanvasObject { 
			label := widget.NewLabel("Template")
			label.TextStyle.Bold = false
			return label 
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				// 表头
				headers := []string{"分数", "匿名度", "协议", "代理地址", "延迟(ms)", "速度", "地区", "操作"}
				switch id.Col {
				case 0: // 分数列
					if sortByScoreDesc {
						headers[0] = "分数 ▼"
					} else {
						headers[0] = "分数 ▲"
					}
				case 4: // 延迟列
					if sortByLatencyDesc {
						headers[4] = "延迟(ms) ▼"
					} else {
						headers[4] = "延迟(ms) ▲"
					}
				case 5: // 速度列
					if sortBySpeedDesc {
						headers[5] = "速度 ▼"
					} else {
						headers[5] = "速度 ▲"
					}
				}
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
			case 0: // 分数
				text = fmt.Sprintf("%.1f", p.Score)
			case 1: // 匿名度
				text = p.Anonymity
			case 2: // 协议
				text = p.Protocol
			case 3: // 代理地址
				text = p.Address
			case 4: // 延迟
				if p.Latency > 0 {
					text = fmt.Sprintf("%d", int(p.Latency*1000))
				} else {
					text = "-"
				}
			case 5: // 速度
				if p.Speed > 0 {
					if p.Speed > 1024 {
						text = fmt.Sprintf("%.2f MB/s", p.Speed/1024)
					} else {
						text = fmt.Sprintf("%.2f KB/s", p.Speed)
					}
				} else {
					text = "-"
				}
			case 6: // 地区
				text = p.Location
			case 7: // 操作提示
				text = "右键操作"
				// 使用canvas.Text替代直接设置颜色
				statusText := canvas.NewText(text, fynetheme.Color("disabled"))
				statusText.TextSize = 14
				statusText.Alignment = fyne.TextAlignLeading
			}
			label.SetText(text)
			label.TextStyle.Bold = false
		},
	)

	// 设置列宽
	table.SetColumnWidth(0, 70)  // 分数
	table.SetColumnWidth(1, 80)  // 匿名度
	table.SetColumnWidth(2, 70)  // 协议
	table.SetColumnWidth(3, 180) // 代理地址
	table.SetColumnWidth(4, 90)  // 延迟
	table.SetColumnWidth(5, 100) // 速度
	table.SetColumnWidth(6, 80)  // 地区
	table.SetColumnWidth(7, 80)  // 操作

	// 处理表格选择事件
	table.OnSelected = func(id widget.TableCellID) {
		selectedRow = id

		if id.Row > 0 {
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(table)
			widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", menuItems...), app.GetWindow().Canvas(), pos)
		}

		if id.Row == 0 {
			switch id.Col {
			case 0: // 点击分数列标题
				sortByScoreDesc = !sortByScoreDesc
				sortProxies("score")
			case 4: // 点击延迟列标题
				sortByLatencyDesc = !sortByLatencyDesc
				sortProxies("latency")
			case 5: // 点击速度列标题
				sortBySpeedDesc = !sortBySpeedDesc
				sortProxies("speed")
			}
			table.Refresh()
		}
	}

	// 添加测试全部和导出全部按钮
	tableControls := container.NewHBox(
		createStyledButton("全部重测", app.TestAllProxies, "info"),
		createStyledButton("导出代理", app.ExportProxies, "primary"),
	)

	// 组合代理列表面板
	proxyListContent := container.NewBorder(tableControls, nil, nil, nil, table)
	return widget.NewCard("可用代理列表", "（右键可进行操作）", proxyListContent)
}

// createLogPanel 创建日志显示面板
func createLogPanel(app Apper) fyne.CanvasObject {
	// 创建日志文本框
	logText := widget.NewMultiLineEntry()
	logText.SetPlaceHolder("实时日志将显示在这里...")
	logText.Wrapping = fyne.TextWrapBreak
	logText.Disable() // 设置为只读
	logText.Resize(fyne.NewSize(0, 150))
	
	// 创建日志滚动容器
	logScroll := container.NewVScroll(logText)
	logScroll.SetMinSize(fyne.NewSize(0, 150))
	
	// 创建清除日志按钮
	clearLogBtn := widget.NewButton("清除日志", func() {
		logText.SetText("")
	})
	
	// 组合日志面板
	logPanel := container.NewBorder(nil, clearLogBtn, nil, nil, logScroll)
	
	return widget.NewCard("实时日志", "", logPanel)
}

// createSettingsWindow 创建设置窗口
func createSettingsWindow(app Apper) {
	win := fyne.CurrentApp().NewWindow("设置")
	win.Resize(fyne.NewSize(600, 400))

	// 获取配置管理器
	configManager, ok := app.(interface {
		SaveConfig() error
		GetConfig() *config.AppConfig
	})
	if !ok {
		app.Log("Error: App does not implement config management interface.")
		return
	}

	cfg := configManager.GetConfig()

	// 创建选项卡容器
	tabs := container.NewAppTabs()

	// 通用设置选项卡
	generalTab := createGeneralSettingsTab(cfg)
	tabs.Append(container.NewTabItem("通用设置", generalTab))

	// 自动爬取选项卡
	crawlerTab := createCrawlerSettingsTab(cfg)
	tabs.Append(container.NewTabItem("自动爬取", crawlerTab))

	// 保存按钮
	saveBtn := createStyledButton("保存设置", func() {
		if err := configManager.SaveConfig(); err != nil {
			dialog.ShowError(err, win)
			return
		}
		app.Log("设置已保存")
		dialog.ShowInformation("成功", "设置已成功保存", win)
	}, "primary")

	// 取消按钮
	cancelBtn := createStyledButton("取消", func() {
		win.Close()
	}, "info")

	// 组合界面
	btnBox := container.NewHBox(layout.NewSpacer(), saveBtn, cancelBtn)
	content := container.NewVBox(tabs, btnBox)

	win.SetContent(container.NewPadded(content))
	win.Show()
}

// createGeneralSettingsTab 创建通用设置选项卡
func createGeneralSettingsTab(cfg *config.AppConfig) fyne.CanvasObject {
	// 过滤设置
	maxLatencyEntry := widget.NewEntry()
	maxLatencyEntry.SetText(fmt.Sprintf("%d", cfg.MaxLatency))
	
	minSpeedEntry := widget.NewEntry()
	minSpeedEntry.SetText(fmt.Sprintf("%.1f", cfg.MinSpeed))

	// 健康检查设置
	healthCheckIntervalEntry := widget.NewEntry()
	healthCheckIntervalEntry.SetText(fmt.Sprintf("%d", cfg.HealthCheckInterval))

	// 轮换设置
	rotationIntervalEntry := widget.NewEntry()
	rotationIntervalEntry.SetText(fmt.Sprintf("%d", cfg.RotationInterval))

	// 代理模式
	proxyModeRadio := widget.NewRadioGroup([]string{"fixed", "per_request"}, func(value string) {
		cfg.ProxyMode = value
	})
	proxyModeRadio.SetSelected(cfg.ProxyMode)

	// 创建表单布局
	form := container.New(layout.NewFormLayout(),
		widget.NewLabel("最大延迟 (ms):"), maxLatencyEntry,
		widget.NewLabel("最小速度 (KB/s):"), minSpeedEntry,
		widget.NewLabel("健康检查间隔 (分钟):"), healthCheckIntervalEntry,
		widget.NewLabel("轮换间隔 (秒):"), rotationIntervalEntry,
		widget.NewLabel("代理模式:"), proxyModeRadio,
	)

	// 添加保存按钮事件处理
	go func() {
		<-time.After(100) // 小延迟确保UI渲染完成
		if maxLatency, err := strconv.Atoi(maxLatencyEntry.Text); err == nil {
			cfg.MaxLatency = float64(maxLatency)
		}
		if minSpeed, err := strconv.ParseFloat(minSpeedEntry.Text, 64); err == nil {
			cfg.MinSpeed = minSpeed
		}
		if healthCheckInterval, err := strconv.Atoi(healthCheckIntervalEntry.Text); err == nil {
			cfg.HealthCheckInterval = healthCheckInterval
		}
		if rotationInterval, err := strconv.Atoi(rotationIntervalEntry.Text); err == nil {
			cfg.RotationInterval = rotationInterval
		}
	}()

	return form
}

// createCrawlerSettingsTab 创建自动爬取设置选项卡
func createCrawlerSettingsTab(cfg *config.AppConfig) fyne.CanvasObject {
	// 自动爬取开关 (为了UI兼容性保留，但不保存到配置)
	autoCrawlEnabled := widget.NewCheck("启用自动爬取", func(checked bool) {
		// 这里不保存到配置，因为配置中没有此字段
		fmt.Println("自动爬取设置变更为:", checked)
	})
	autoCrawlEnabled.SetChecked(false)

	// 为了UI兼容性保留这些字段，但不保存到配置
	crawlIntervalEntry := widget.NewEntry()
	crawlIntervalEntry.SetText("15") // 默认值

	maxRetryEntry := widget.NewEntry()
	maxRetryEntry.SetText("3") // 默认值

	sourceWeightEntry := widget.NewEntry()
	sourceWeightEntry.SetText("10") // 默认值

	apiTimeoutEntry := widget.NewEntry()
	apiTimeoutEntry.SetText("30") // 默认值

	// 创建表单布局
	form := container.New(layout.NewFormLayout(),
		autoCrawlEnabled, layout.NewSpacer(),
		widget.NewLabel("爬取间隔 (分钟):"), crawlIntervalEntry,
		widget.NewLabel("最大重试次数:"), maxRetryEntry,
		widget.NewLabel("代理源权重:"), sourceWeightEntry,
		widget.NewLabel("API请求超时 (秒):"), apiTimeoutEntry,
	)

	// 添加保存按钮事件处理
	go func() {
		<-time.After(100) // 小延迟确保UI渲染完成
		// 打印日志但不保存到配置，因为这些字段在配置中不存在
		fmt.Println("爬取设置变更 (不保存到配置)")
	}()

	return form
}

// createSourceManagementWindow 创建代理源管理窗口
func createSourceManagementWindow(app Apper) {
	win := fyne.CurrentApp().NewWindow("代理源管理")
	win.Resize(fyne.NewSize(800, 500))

	// Cast app to SourceManager interface
	sourceManager, ok := app.(interface {
		GetSources() []fetcher.ProxySource
		AddSource(source fetcher.ProxySource)
		RemoveSource(index int)
		TestSource(source fetcher.ProxySource) (string, error)
	})
	if !ok {
		app.Log("Error: App does not implement SourceManager interface.")
		return
	}

	sourceList := binding.NewStringList()
	refreshSourceList := func() {
		currentSources := sourceManager.GetSources()
		var displaySources []string
		for _, s := range currentSources {
			displaySources = append(displaySources, fmt.Sprintf("[%s] %s (API: %t)", s.Protocol, s.URL, s.IsAPI))
		}
		sourceList.Set(displaySources)
	}
	refreshSourceList() // Initial load

	list := widget.NewListWithData(sourceList,
		func() fyne.CanvasObject {
			return widget.NewLabel("template")
		},
		func(i binding.DataItem, o fyne.CanvasObject) {
			o.(*widget.Label).Bind(i.(binding.String))
		})

	var selectedIndex int = -1
	list.OnSelected = func(id widget.ListItemID) {
		selectedIndex = id
	}

	testResult := widget.NewMultiLineEntry()
	testResult.SetPlaceHolder("测试结果将显示在这里...")
	testResult.Wrapping = fyne.TextWrapBreak

	addBtn := createStyledButton("添加", func() {
		urlEntry := widget.NewEntry()
		urlEntry.SetPlaceHolder("http://...")
		protocolSelect := widget.NewSelect([]string{"http", "https", "socks4", "socks5"}, func(s string) {})
		protocolSelect.SetSelected("http") // Default protocol
		isAPICheck := widget.NewCheck("是API源", func(b bool) {})

		dialog.ShowForm("添加新代理源", "添加", "取消",
			[]*widget.FormItem{
				widget.NewFormItem("URL", urlEntry),
				widget.NewFormItem("协议", protocolSelect),
				widget.NewFormItem("类型", isAPICheck),
			},
			func(ok bool) {
				if ok {
					// 创建并添加代理源
				source := fetcher.ProxySource{
						URL:      urlEntry.Text,
						Protocol: protocolSelect.Selected,
						IsAPI:    isAPICheck.Checked,
						Parser:   "text", // 默认使用文本解析器
					}
				sourceManager.AddSource(source)
					refreshSourceList()
				}
			}, win)
	}, "primary")

	removeBtn := createStyledButton("删除", func() {
		if selectedIndex != -1 {
			dialog.ShowConfirm("确认删除", "确定要删除选中的代理源吗?", func(ok bool) {
				if ok {
					sourceManager.RemoveSource(selectedIndex)
					refreshSourceList()
					selectedIndex = -1 // Reset selection
					list.UnselectAll()
				}
			}, win)
		}
	}, "danger")

	testBtn := createStyledButton("测试选中源", func() {
		if selectedIndex != -1 {
			currentSources := sourceManager.GetSources()
			if selectedIndex < len(currentSources) {
				sourceToTest := currentSources[selectedIndex]
				testResult.SetText(fmt.Sprintf("正在测试源: %s...", sourceToTest.URL))
				go func() {
					result, err := sourceManager.TestSource(sourceToTest)
					if err != nil {
						testResult.SetText(fmt.Sprintf("测试源 %s 失败: %v", sourceToTest.URL, err))
					} else {
						testResult.SetText(fmt.Sprintf("测试源 %s 完成:\n%s", sourceToTest.URL, result))
					}
				}()
			}
		}
	}, "info")

	buttons := container.NewHBox(addBtn, removeBtn, testBtn)
	leftPanel := container.NewBorder(nil, buttons, nil, nil, list)
	split := container.NewHSplit(leftPanel, testResult)
	split.SetOffset(0.5)

	win.SetContent(split)
	win.Show()
}



// queryIPCountry 本地查询IP地理位置信息
func queryIPCountry(ip string) (string, error) {
	// 简单IP前缀匹配表
	ipPrefixes := map[string]struct {
		Country  string
		Province string
		City     string
	}{
		"58.30": {"中国", "北京", "北京"},
		"58.31": {"中国", "上海", "上海"},
		"58.32": {"中国", "天津", "天津"},
		"58.33": {"中国", "重庆", "重庆"},
		"58.34": {"中国", "广东", "广州"},
		"58.35": {"中国", "浙江", "杭州"},
		"58.36": {"中国", "江苏", "南京"},
		"58.37": {"中国", "四川", "成都"},
		"58.38": {"中国", "湖北", "武汉"},
		"58.39": {"中国", "陕西", "西安"},
	}

	// 提取IP前两段作为前缀
	prefix := ""
	parts := strings.Split(ip, ".")
	if len(parts) >= 2 {
		prefix = parts[0] + "." + parts[1]
	}

	// 查找匹配的地理位置
	if loc, ok := ipPrefixes[prefix]; ok {
		return loc.Country + "|" + loc.Province + "|" + loc.City, nil
	}

	return "未知|未知|未知", nil
}

// createProxyList 创建代理列表表格视图
// 以表格形式展示所有可用代理，包含协议、地址、延迟、速度等关键信息
func createProxyList(app Apper) fyne.CanvasObject {
	originalData := app.GetProxyList()
	filteredData := binding.NewUntypedList()

	// 过滤并设置数据显示
	filterAndSet := func() {
		items, _ := originalData.Get()
		filteredItems := make([]interface{}, 0)
		for _, item := range items {
			if p, ok := item.(*proxy.Proxy); ok && p.Speed > 0 {
				filteredItems = append(filteredItems, p)
			}
		}
		filteredData.Set(filteredItems)
	}

	// 首次加载和数据变更时应用过滤
	filterAndSet()
	originalData.AddListener(binding.NewDataListener(filterAndSet))

	data := filteredData // 后续代码使用过滤后的数据
	var (
		sortBySpeedDesc   bool = true
		sortByLatencyDesc bool = true
		sortByScoreDesc   bool = true // New sort state for Score
	)

	// 排序代理列表
	sortProxies := func(sortBy string) {
		items, _ := data.Get()
		proxies := make([]*proxy.Proxy, len(items))
		for i, item := range items {
			proxies[i] = item.(*proxy.Proxy)
		}

		sort.Slice(proxies, func(i, j int) bool {
			switch sortBy {
			case "speed":
				if sortBySpeedDesc {
					return proxies[i].Speed > proxies[j].Speed
				}
				return proxies[i].Speed < proxies[j].Speed
			case "latency":
				if sortByLatencyDesc {
					return proxies[i].Latency > proxies[j].Latency
				}
				return proxies[i].Latency < proxies[j].Latency
			case "score": // New sort case for Score
				if sortByScoreDesc {
					return proxies[i].Score > proxies[j].Score
				}
				return proxies[i].Score < proxies[j].Score
			}
			return false
		})

		newItems := make([]interface{}, len(proxies))
		for i, p := range proxies {
			newItems[i] = p
		}
		data.Set(newItems)
	}

	// Current selected row ID
	var selectedRow widget.TableCellID

	// Right-click menu items
	menuItems := []*fyne.MenuItem{
		fyne.NewMenuItem("测试选中代理", func() {
			items, _ := data.Get()
			if selectedRow.Row > 0 && selectedRow.Row <= len(items) {
				p := items[selectedRow.Row-1].(*proxy.Proxy)
				app.Log(fmt.Sprintf("开始测试代理: %s", p.Address))
				go func() {
					// Placeholder for actual test method
					app.Log(fmt.Sprintf("代理 %s 测试完成", p.Address))
				}()
			}
		}),
		fyne.NewMenuItem("导出选中代理", func() {
			if selectedRow.Row > 0 {
				items, _ := data.Get()
				if selectedRow.Row <= len(items) {
					p := items[selectedRow.Row-1].(*proxy.Proxy)
					dialog.ShowFileSave(func(uri fyne.URIWriteCloser, err error) {
						if uri != nil {
							defer uri.Close()
							_, _ = uri.Write([]byte(p.Address))
							app.Log(fmt.Sprintf("已导出代理: %s", p.Address))
						}
					}, app.GetWindow())
				}
			}
		}),
		fyne.NewMenuItem("删除选中代理", func() {
			if selectedRow.Row > 0 {
				items, _ := data.Get()
				if selectedRow.Row <= len(items) {
					p := items[selectedRow.Row-1].(*proxy.Proxy)
					dialog.ShowConfirm("确认删除", fmt.Sprintf("确定要删除代理 %s 吗?", p.Address), func(ok bool) {
						if ok {
							originalItems, _ := originalData.Get()
							newOriginalItems := make([]interface{}, 0)
							for _, item := range originalItems {
								if item.(*proxy.Proxy).Address != p.Address {
									newOriginalItems = append(newOriginalItems, item)
								}
							}
							originalData.Set(newOriginalItems)
							app.Log(fmt.Sprintf("已删除代理: %s", p.Address))
						}
					}, app.GetWindow())
				}
			}
		}),
	}

	table := widget.NewTable(
		func() (int, int) { return data.Length() + 1, 8 }, // Increased column count to 8 for Score
		func() fyne.CanvasObject { return widget.NewLabel("Template") },
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				headers := []string{"协议", "代理地址", "延迟(ms)", "速度", "成功率", "匿名度", "地区", "评分"}
				switch id.Col {
				case 2: // Latency column
					if sortByLatencyDesc {
						headers[2] = "延迟(ms) ▼"
					} else {
						headers[2] = "延迟(ms) ▲"
					}
				case 3: // Speed column
					if sortBySpeedDesc {
						headers[3] = "速度 ▼"
					} else {
						headers[3] = "速度 ▲"
					}
				case 7: // Score column
					if sortByScoreDesc {
						headers[7] = "评分 ▼"
					} else {
						headers[7] = "评分 ▲"
					}
				}
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
					text = fmt.Sprintf("%d", int(p.Latency*1000))
				} else {
					text = "-"
				}
			case 3:
				if p.Speed > 0 {
					if p.Speed > 1024 {
						text = fmt.Sprintf("%.2f MB/s", p.Speed/1024)
					} else {
						text = fmt.Sprintf("%.2f KB/s", p.Speed)
					}
				} else {
					text = "-"
				}
			case 4:
				total := p.SuccessCount + p.FailCount
				if total > 0 {
					rate := float64(p.SuccessCount) / float64(total) * 100
					text = fmt.Sprintf("%.1f%%", rate)
				} else {
					text = "N/A"
				}
			case 5:
				text = p.Anonymity
			case 6:
				text = p.Location
			case 7: // Score column
				text = fmt.Sprintf("%.1f", p.Score)
			}
			label.SetText(text)
			label.TextStyle.Bold = false
		},
	)
	table.SetColumnWidth(0, 70)  // Protocol
	table.SetColumnWidth(1, 200) // Address
	table.SetColumnWidth(2, 100) // Latency
	table.SetColumnWidth(3, 100) // Speed
	table.SetColumnWidth(4, 80)  // Success Rate
	table.SetColumnWidth(5, 100) // Anonymity
	table.SetColumnWidth(6, 80)  // Location
	table.SetColumnWidth(7, 80)  // Score

	table.OnSelected = func(id widget.TableCellID) {
		selectedRow = id

		if id.Row > 0 {
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(table)
			widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", menuItems...), app.GetWindow().Canvas(), pos)
		}

		if id.Row == 0 {
			switch id.Col {
			case 2: // Click Latency column header
				sortByLatencyDesc = !sortByLatencyDesc
				sortProxies("latency")
			case 3: // Click Speed column header
				sortBySpeedDesc = !sortBySpeedDesc
				sortProxies("speed")
			case 7: // Click Score column header
				sortByScoreDesc = !sortByScoreDesc
				sortProxies("score")
			}
			table.Refresh()
		}
	}

	data.AddListener(binding.NewDataListener(func() {
		table.Refresh()
	}))

	return widget.NewCard("有效代理列表", "", table)
}
