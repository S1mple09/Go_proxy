package theme

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
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

// Color 返回自定义主题的颜色
func (m *MyTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameSeparator:
		if variant == theme.VariantDark {
			return color.NRGBA{R: 80, G: 80, B: 80, A: 255} // 深色主题下更明显的分割线
		}
		return color.NRGBA{R: 200, G: 200, B: 200, A: 255} // 浅色主题分割线
	case theme.ColorNameBackground:
		if variant == theme.VariantDark {
			return color.NRGBA{R: 30, G: 30, B: 30, A: 255} // 深色背景
		}
	case theme.ColorNameForeground:
		if variant == theme.VariantDark {
			return color.NRGBA{R: 220, G: 220, B: 220, A: 255} // 深色前景
		}
	case theme.ColorNameButton:
		if variant == theme.VariantDark {
			return color.NRGBA{R: 60, G: 60, B: 60, A: 255} // 深色按钮
		}
	}
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
