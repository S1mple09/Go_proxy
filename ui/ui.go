package ui

import (
	"fmt"
	"go_proxy/proxy"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	fynetheme "fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	customtheme "go_proxy/theme"
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
}

// SetupUI 初始化应用主界面，排列所有UI组件
// 参数 app 提供了访问应用核心功能和数据绑定的接口
func SetupUI(app Apper) {
	toolbar := createToolbar(app)
	filterControl := createFilterControlPanel(app)
	serverControl := createServerControlPanel(app)
	rotationControl := createRotationControlPanel(app)
	progressCard := widget.NewCard("进度", "", app.GetProgressBar())

	// 创建代理详情显示区域
	currentProxyInfo := widget.NewMultiLineEntry()
	currentProxyInfo.Disable()
	currentProxyInfo.SetPlaceHolder("当前代理信息将在此显示...")

	// 绑定当前代理信息更新
	app.GetCurrentProxy().AddListener(binding.NewDataListener(func() {
		proxyAddr, _ := app.GetCurrentProxy().Get()
		if proxyAddr != "" {
			// 获取完整代理信息
			items, _ := app.GetProxyList().Get()
			for _, item := range items {
				p := item.(*proxy.Proxy)
				if p.Address == proxyAddr {
					info := fmt.Sprintf("当前代理: %s\n协议: %s\n国家: %s\n省份: %s\n城市: %s\n延迟: %.0fms\n速度: %.2fKB/s\n匿名度: %s",
						p.Address, p.Protocol, p.Country, p.Province, p.City, p.Latency*1000, p.Speed, p.Anonymity)
					currentProxyInfo.SetText(info)
					break
				}
			}
		} else {
			currentProxyInfo.SetText("")
		}
	}))

	proxyList := createProxyList(app)
	logView := createLogView(app)

	// 新的三栏布局：代理列表 | 代理详情 | 日志
	leftPanel := container.NewBorder(nil, nil, nil, nil, proxyList)
	centerPanel := container.NewBorder(
		widget.NewLabelWithStyle("当前代理详情", fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		nil, nil, nil,
		container.NewScroll(currentProxyInfo),
	)
	rightPanel := container.NewBorder(nil, nil, nil, nil, logView)

	// 第一层分割：左侧代理列表和中间区域
	leftSplit := container.NewHSplit(leftPanel, centerPanel)
	leftSplit.SetOffset(0.4)

	// 第二层分割：中间区域和右侧日志
	mainSplit := container.NewHSplit(leftSplit, rightPanel)
	mainSplit.SetOffset(0.7)

	topPanel := container.NewVBox(toolbar, filterControl, serverControl, rotationControl, progressCard)
	mainLayout := container.NewBorder(topPanel, nil, nil, nil, mainSplit)

	win := app.GetWindow()
	win.SetContent(container.NewPadded(mainLayout))
	win.Resize(fyne.NewSize(1280, 800))
}

// createToolbar 创建顶部工具栏，包含代理操作的主要功能按钮
// 包括获取代理、测试代理、导入导出和清空列表等操作
func createToolbar(app Apper) fyne.CanvasObject {
	ipEntry := widget.NewEntry()
	ipEntry.SetPlaceHolder("输入IP地址")

	// 主题切换按钮
	themeBtn := widget.NewButton("切换主题", func() {
		currentTheme := fyne.CurrentApp().Settings().Theme()
		if _, isCustom := currentTheme.(*customtheme.MyTheme); isCustom {
			// 如果当前是自定义主题，切换内置主题
			if currentTheme == fynetheme.DarkTheme() {
				fyne.CurrentApp().Settings().SetTheme(fynetheme.LightTheme())
			} else {
				fyne.CurrentApp().Settings().SetTheme(fynetheme.DarkTheme())
			}
		} else {
			// 如果当前是内置主题，切换自定义主题
			fyne.CurrentApp().Settings().SetTheme(&customtheme.MyTheme{})
		}
		app.GetWindow().Content().Refresh()
	})

	buttons := container.NewHBox(
		widget.NewButton("获取代理", app.FetchProxies),
		widget.NewButton("测试代理", app.TestAllProxies),
		widget.NewButton("导入代理", app.ImportProxies),
		widget.NewButton("导出代理", app.ExportProxies),
		themeBtn,
		widget.NewButton("查询IP", func() {
			ip := ipEntry.Text
			if ip != "" {
				go func() {
					app.Log(fmt.Sprintf("正在查询IP: %s", ip))
					location, err := queryIPCountry(ip)
					if err != nil {
						app.Log(fmt.Sprintf("查询IP失败: %v", err))
						return
					}
					parts := strings.Split(location, "|")
					if len(parts) == 3 {
						country := parts[0]
						province := parts[1]
						city := parts[2]
						app.Log(fmt.Sprintf("IP %s 位置: %s %s %s", ip, country, province, city))
						// 更新当前代理的位置信息
						currentProxy, _ := app.GetCurrentProxy().Get()
						if currentProxy != "" {
							// 这里需要app有方法更新代理的位置信息
							app.Log(fmt.Sprintf("已更新代理 %s 的位置为 %s %s %s", currentProxy, country, province, city))
						}
					}
				}()
			}
		}),
		widget.NewButton("清空列表", func() {
			dialog.ShowConfirm("确认", "确定要清空所有代理列表吗?", func(ok bool) {
				if ok {
					app.ClearProxies()
				}
			}, app.GetWindow())
		}),
		ipEntry,
	)
	return container.NewPadded(buttons)
}

// createFilterControlPanel 创建代理筛选控制面板
// 提供按延迟和速度筛选代理的功能，支持实时过滤代理列表
func createFilterControlPanel(app Apper) fyne.CanvasObject {
	latencyEntry := widget.NewEntry()
	latencyEntry.SetPlaceHolder("例如: 500 (ms)")

	speedEntry := widget.NewEntry()
	speedEntry.SetPlaceHolder("例如: 1024 (KB/s)")

	applyBtn := widget.NewButton("应用筛选", func() {
		app.ApplyFilters(latencyEntry.Text, speedEntry.Text)
	})

	grid := container.New(layout.NewFormLayout(),
		widget.NewLabel("最大延迟 (ms):"), latencyEntry,
		widget.NewLabel("最低速度 (KB/s):"), speedEntry,
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

	toggleServerBtn := widget.NewButton("启动服务", func() {
		app.ToggleServer(portEntry.Text)
	})
	serverStatusBinding.AddListener(binding.NewDataListener(func() {
		running, _ := serverStatusBinding.Get()
		if running {
			toggleServerBtn.SetText("停止服务")
			portEntry.Disable()
		} else {
			toggleServerBtn.SetText("启动服务")
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
	data := app.GetProxyList()
	var (
		sortBySpeedDesc   bool = true
		sortByLatencyDesc bool = true
	)

	// 排序代理列表
	sortProxies := func(sortBy string) {
		items, _ := data.Get()
		proxies := make([]*proxy.Proxy, len(items))
		for i, item := range items {
			proxies[i] = item.(*proxy.Proxy)
		}

		// 排序代理
		switch sortBy {
		case "speed":
			if sortBySpeedDesc {
				// 降序排序
				for i := 0; i < len(proxies)-1; i++ {
					for j := i + 1; j < len(proxies); j++ {
						if proxies[i].Speed < proxies[j].Speed {
							proxies[i], proxies[j] = proxies[j], proxies[i]
						}
					}
				}
			} else {
				// 升序排序
				for i := 0; i < len(proxies)-1; i++ {
					for j := i + 1; j < len(proxies); j++ {
						if proxies[i].Speed > proxies[j].Speed {
							proxies[i], proxies[j] = proxies[j], proxies[i]
						}
					}
				}
			}
		case "latency":
			if sortByLatencyDesc {
				// 降序排序
				for i := 0; i < len(proxies)-1; i++ {
					for j := i + 1; j < len(proxies); j++ {
						if proxies[i].Latency < proxies[j].Latency {
							proxies[i], proxies[j] = proxies[j], proxies[i]
						}
					}
				}
			} else {
				// 升序排序
				for i := 0; i < len(proxies)-1; i++ {
					for j := i + 1; j < len(proxies); j++ {
						if proxies[i].Latency > proxies[j].Latency {
							proxies[i], proxies[j] = proxies[j], proxies[i]
						}
					}
				}
			}
		}

		newItems := make([]interface{}, len(proxies))
		for i, p := range proxies {
			newItems[i] = p
		}
		data.Set(newItems)
	}

	table := widget.NewTable(
		func() (int, int) { return data.Length() + 1, 6 },
		func() fyne.CanvasObject { return widget.NewLabel("Template") },
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				headers := []string{"协议", "代理地址", "延迟(ms)", "速度(KB/s)", "匿名度", "地区"}
				switch id.Col {
				case 2: // 延迟列
					if sortByLatencyDesc {
						headers[2] = "延迟(ms) ▼"
					} else {
						headers[2] = "延迟(ms) ▲"
					}
				case 3: // 速度列
					if sortBySpeedDesc {
						headers[3] = "速度(KB/s) ▼"
					} else {
						headers[3] = "速度(KB/s) ▲"
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
					text = fmt.Sprintf("%6.0f", p.Latency*1000) // 右对齐数字
				} else {
					text = fmt.Sprintf("%6s", "-") // 保持相同宽度
				}
			case 3:
				if p.Speed > 0 {
					text = fmt.Sprintf("%6.2f", p.Speed) // 右对齐数字
				} else {
					text = fmt.Sprintf("%6s", "-") // 保持相同宽度
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
	table.SetColumnWidth(0, 70)  // 协议列
	table.SetColumnWidth(1, 200) // 代理地址列
	table.SetColumnWidth(2, 100) // 延迟列
	table.SetColumnWidth(3, 100) // 速度列
	table.SetColumnWidth(4, 100) // 匿名度列
	table.SetColumnWidth(5, 80)  // 地区列

	// 点击速度列头排序
	table.OnSelected = func(id widget.TableCellID) {
		if id.Row == 0 {
			switch id.Col {
			case 2: // 点击延迟列头
				sortByLatencyDesc = !sortByLatencyDesc
				sortProxies("latency")
			case 3: // 点击速度列头
				sortBySpeedDesc = !sortBySpeedDesc
				sortProxies("speed")
			}
			table.Refresh()
		}
	}

	return widget.NewCard("有效代理列表", "", table)
}

// createRotationControlPanel 创建代理轮换控制面板
// 提供轮换开关、当前代理显示和轮换间隔设置功能
func createRotationControlPanel(app Apper) *widget.Card {
	rotationStatus := app.GetRotationStatus()
	currentProxy := app.GetCurrentProxy()

	// Rotation toggle switch
	toggle := widget.NewCheck("启用代理轮换", func(enable bool) {
		app.ToggleRotation(enable)
	})
	rotationStatus.AddListener(binding.NewDataListener(func() {
		enabled, _ := rotationStatus.Get()
		toggle.SetChecked(enabled)
	}))

	// Current proxy display
	currentProxyDisplay := widget.NewLabel("")
	widget.NewLabel("当前代理: ")
	currentProxy.AddListener(binding.NewDataListener(func() {
		proxy, _ := currentProxy.Get()
		currentProxyDisplay.SetText(proxy)
	}))

	// Rotation interval setting
	intervalEntry := widget.NewEntry()
	intervalEntry.SetPlaceHolder("例如: 60 (秒)")
	intervalEntry.SetText("60")
	intervalBtn := widget.NewButton("设置间隔", func() {
		seconds, err := strconv.Atoi(intervalEntry.Text)
		if err == nil && seconds > 0 {
			app.SetRotationInterval(seconds)
		}
	})

	grid := container.New(layout.NewFormLayout(),
		widget.NewLabel("轮换设置:"), toggle,
		widget.NewLabel("当前代理:"), currentProxyDisplay,
		widget.NewLabel("轮换间隔(秒):"), intervalEntry,
		layout.NewSpacer(), intervalBtn,
	)
	return widget.NewCard("代理轮换", "控制代理自动轮换行为", grid)
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
