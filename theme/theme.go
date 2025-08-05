package theme

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
	"image/color"
)

// MyTheme 定义了自定义主题
type MyTheme struct{}

// 确保 MyTheme 实现了 fyne.Theme 接口
var _ fyne.Theme = (*MyTheme)(nil)

// Font 返回我们捆绑的中文字体
// resourceFontTtf 变量是在 bundled.go 文件中由 'fyne bundle' 命令自动生成的
func (m *MyTheme) Font(style fyne.TextStyle) fyne.Resource {
	return resourceFontTtf
}

// Color 返回默认主题的颜色
func (m *MyTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	return theme.DefaultTheme().Color(name, variant)
}

// Icon 返回默认主题的图标
func (m *MyTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

// Size 返回默认主题的尺寸
func (m *MyTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}
