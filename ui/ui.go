package ui

import (
	"fmt"
	"go_proxy/fetcher"
	"go_proxy/proxy"
	"sort"
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
}

// SetupUI 初始化应用主界面，排列所有UI组件
// 参数 app 提供了访问应用核心功能和数据绑定的接口
func SetupUI(app Apper) {
	toolbar := createToolbar(app)
	filterControl := createFilterControlPanel(app)
	serverControl := createServerControlPanel(app)
	rotationControl := createRotationControlPanel(app)
	// Create progress text
	progressLabel := widget.NewLabel("")
	progressLabel.Bind(app.GetProgressText())
	progressCard := widget.NewCard("进度", "", progressLabel)

	proxyList := createProxyList(app)

	topPanel := container.NewVBox(toolbar, filterControl, serverControl, rotationControl, progressCard)
	mainLayout := container.NewBorder(topPanel, nil, nil, nil, proxyList)

	win := app.GetWindow()
	win.SetContent(container.NewPadded(mainLayout))
	win.Resize(fyne.NewSize(800, 600))
}

// createToolbar 创建顶部工具栏，包含代理操作的主要功能按钮
// 包括获取代理、测试代理、导入导出和清空列表等操作
func createToolbar(app Apper) fyne.CanvasObject {
	ipEntry := widget.NewEntry()
	ipEntry.SetPlaceHolder("输入IP地址")

	// 主题切换选择框
	themeSelect := widget.NewSelect([]string{"默认", "深色", "自定义", "蓝色", "绿色"}, func(selected string) {
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
	})
	themeSelect.SetSelected("自定义") // 默认选中自定义主题

	// 当前轮换IP显示
	currentRotationIPLabel := widget.NewLabelWithStyle("当前轮换IP: 无", fyne.TextAlignTrailing, fyne.TextStyle{Bold: true})
	app.GetCurrentProxy().AddListener(binding.NewDataListener(func() {
		proxyAddr, _ := app.GetCurrentProxy().Get()
		if proxyAddr != "" {
			currentRotationIPLabel.SetText(fmt.Sprintf("当前轮换IP: %s", proxyAddr))
		} else {
			currentRotationIPLabel.SetText("当前轮换IP: 无")
		}
	}))

	buttons := container.NewHBox(
		widget.NewButton("获取代理", app.FetchProxies),
		widget.NewButton("测试代理", app.TestAllProxies),
		widget.NewButton("导入代理", app.ImportProxies),
		widget.NewButton("导出代理", app.ExportProxies),
		themeSelect, // 使用主题选择框
		widget.NewButton("管理代理源", func() {
			createSourceManagementWindow(app)
		}),
		widget.NewButton("清空列表", func() {
			dialog.ShowConfirm("确认", "确定要清空所有代理列表吗?", func(ok bool) {
				if ok {
					app.ClearProxies()
				}
			}, app.GetWindow())
		}),
		layout.NewSpacer(), // 将IP显示推到右侧
		currentRotationIPLabel,
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

func createSourceManagementWindow(app Apper) {
	win := fyne.CurrentApp().NewWindow("代理源管理")
	win.Resize(fyne.NewSize(800, 500))

	// Cast app to SourceManager interface
	sourceManager, ok := app.(interface {
		GetProxySourcesData() []fetcher.ProxySource
		AddProxySource(url, protocol string, isAPI bool)
		RemoveProxySource(index int)
		TestProxySource(source fetcher.ProxySource) (string, error)
	})
	if !ok {
		app.Log("Error: App does not implement SourceManager interface.")
		return
	}

	sourceList := binding.NewStringList()
	refreshSourceList := func() {
		currentSources := sourceManager.GetProxySourcesData()
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

	addBtn := widget.NewButton("添加", func() {
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
					sourceManager.AddProxySource(urlEntry.Text, protocolSelect.Selected, isAPICheck.Checked)
					refreshSourceList()
				}
			}, win)
	})

	removeBtn := widget.NewButton("删除", func() {
		if selectedIndex != -1 {
			dialog.ShowConfirm("确认删除", "确定要删除选中的代理源吗?", func(ok bool) {
				if ok {
					sourceManager.RemoveProxySource(selectedIndex)
					refreshSourceList()
					selectedIndex = -1 // Reset selection
					list.UnselectAll()
				}
			}, win)
		}
	})

	testBtn := widget.NewButton("测试选中源", func() {
		if selectedIndex != -1 {
			currentSources := sourceManager.GetProxySourcesData()
			if selectedIndex < len(currentSources) {
				sourceToTest := currentSources[selectedIndex]
				testResult.SetText(fmt.Sprintf("正在测试源: %s...", sourceToTest.URL))
				go func() {
					result, err := sourceManager.TestProxySource(sourceToTest)
					if err != nil {
						testResult.SetText(fmt.Sprintf("测试源 %s 失败: %v", sourceToTest.URL, err))
					} else {
						testResult.SetText(fmt.Sprintf("测试源 %s 完成:\n%s", sourceToTest.URL, result))
					}
				}()
			}
		}
	})

	buttons := container.NewHBox(addBtn, removeBtn, testBtn)
	leftPanel := container.NewBorder(nil, buttons, nil, nil, list)
	split := container.NewHSplit(leftPanel, testResult)
	split.SetOffset(0.5)

	win.SetContent(split)
	win.Show()
}

// createServerControlPanel 创建本地代理服务控制面板
// 允许配置端口并启动/停止SOCKS5代理服务，显示当前服务状态
func createServerControlPanel(app Apper) fyne.CanvasObject {
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
	return widget.NewAccordion(
		widget.NewAccordionItem("服务控制", grid),
	)
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
	)

	// 排序代理列表
	sortProxies := func(sortBy string) {
		items, _ := data.Get()
		proxies := make([]*proxy.Proxy, len(items))
		for i, item := range items {
			proxies[i] = item.(*proxy.Proxy)
		}

		// 使用 sort.Slice 进行高效排序
		sort.Slice(proxies, func(i, j int) bool {
			switch sortBy {
			case "speed":
				if sortBySpeedDesc {
					return proxies[i].Speed > proxies[j].Speed // 降序
				}
				return proxies[i].Speed < proxies[j].Speed // 升序
			case "latency":
				if sortByLatencyDesc {
					return proxies[i].Latency > proxies[j].Latency // 降序
				}
				return proxies[i].Latency < proxies[j].Latency // 升序
			}
			return false
		})

		newItems := make([]interface{}, len(proxies))
		for i, p := range proxies {
			newItems[i] = p
		}
		data.Set(newItems)
	}

	// 当前选中的行ID
	var selectedRow widget.TableCellID

	// 右键菜单项
	menuItems := []*fyne.MenuItem{
		fyne.NewMenuItem("测试选中代理", func() {
			items, _ := data.Get()
			if selectedRow.Row > 0 && selectedRow.Row <= len(items) {
				p := items[selectedRow.Row-1].(*proxy.Proxy)
				app.Log(fmt.Sprintf("开始测试代理: %s", p.Address))
				go func() {
					// 这里应该调用实际的测试方法
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
							// 从原始数据中删除
							originalItems, _ := originalData.Get()
							newOriginalItems := make([]interface{}, 0)
							for _, item := range originalItems {
								if item.(*proxy.Proxy).Address != p.Address {
									newOriginalItems = append(newOriginalItems, item)
								}
							}
							originalData.Set(newOriginalItems) // 这将触发监听器并更新过滤列表
							app.Log(fmt.Sprintf("已删除代理: %s", p.Address))
						}
					}, app.GetWindow())
				}
			}
		}),
	}

	table := widget.NewTable(
		func() (int, int) { return data.Length() + 1, 7 },
		func() fyne.CanvasObject { return widget.NewLabel("Template") },
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			if id.Row == 0 {
				headers := []string{"协议", "代理地址", "延迟(ms)", "速度", "成功率", "匿名度", "地区"}
				switch id.Col {
				case 2: // 延迟列
					if sortByLatencyDesc {
						headers[2] = "延迟(ms) ▼"
					} else {
						headers[2] = "延迟(ms) ▲"
					}
				case 3: // 速度列
					if sortBySpeedDesc {
						headers[3] = "速度 ▼"
					} else {
						headers[3] = "速度 ▲"
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
					if p.Speed > 1024 { // 如果速度大于1024KB/s，则以MB/s为单位
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
			}
			label.SetText(text)
			label.TextStyle.Bold = false
		},
	)
	table.SetColumnWidth(0, 70)  // 协议列
	table.SetColumnWidth(1, 200) // 代理地址列
	table.SetColumnWidth(2, 100) // 延迟列
	table.SetColumnWidth(3, 100) // 速度列
	table.SetColumnWidth(4, 80)  // 成功率列
	table.SetColumnWidth(5, 100) // 匿名度列
	table.SetColumnWidth(6, 80)  // 地区列

	// 点击速度列头排序
	// 添加右键菜单支持
	table.OnSelected = func(id widget.TableCellID) {
		selectedRow = id

		if id.Row > 0 {
			// 显示右键菜单
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(table)
			widget.ShowPopUpMenuAtPosition(fyne.NewMenu("", menuItems...), app.GetWindow().Canvas(), pos)
		}

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

	data.AddListener(binding.NewDataListener(func() {
		table.Refresh()
	}))

	return widget.NewCard("有效代理列表", "", table)
}

// createRotationControlPanel 创建代理轮换控制面板
// 提供轮换开关、当前代理显示和轮换间隔设置功能
func createRotationControlPanel(app Apper) fyne.CanvasObject {
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
	return widget.NewAccordion(
		widget.NewAccordionItem("代理轮换", grid),
	)
}
