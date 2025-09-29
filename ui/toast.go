package ui

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// ToastManager manages toast notifications
type ToastManager struct {
	window    fyne.Window
	container *fyne.Container
	queue     []*toast
}

type toast struct {
	message  string
	duration time.Duration
}

// NewToastManager creates a new toast notification manager
func NewToastManager(window fyne.Window) *ToastManager {
	tm := &ToastManager{
		window:    window,
		container: container.NewVBox(),
		queue:     make([]*toast, 0),
	}

	// Position container at bottom-right
	overlay := container.NewWithoutLayout(tm.container)
	window.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		if e.Name == fyne.KeyEscape {
			tm.clearAll()
		}
	})
	window.Canvas().Overlays().Add(overlay)

	return tm
}

// Show displays a toast notification
func (tm *ToastManager) Show(message string, duration time.Duration) {
	t := &toast{
		message:  message,
		duration: duration,
	}
	tm.queue = append(tm.queue, t)
	tm.processQueue()
}

func (tm *ToastManager) processQueue() {
	if len(tm.queue) == 0 {
		return
	}

	t := tm.queue[0]
	tm.queue = tm.queue[1:]

	// Create toast UI
	label := widget.NewLabel(t.message)
	card := widget.NewCard("", "", label)
	card.Resize(fyne.NewSize(300, card.MinSize().Height))

	// Position at bottom-right
	pos := fyne.NewPos(
		tm.window.Canvas().Size().Width-320,
		tm.window.Canvas().Size().Height-float32(len(tm.container.Objects)*40)-40,
	)
	card.Move(pos)

	tm.container.Add(card)
	tm.container.Refresh()

	// Auto-dismiss after duration
	go func() {
		time.Sleep(t.duration)
		tm.container.Remove(card)
		tm.container.Refresh()
		tm.processQueue()
	}()
}

func (tm *ToastManager) clearAll() {
	tm.container.Objects = nil
	tm.container.Refresh()
	tm.queue = make([]*toast, 0)
	tm.window.Canvas().Overlays().Remove(tm.container)
}
