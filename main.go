// Step 1: go get fyne.io/fyne/v2@latest
//		   go get github.com/gopxl/beep@latest
// 		   go get github.com/gopxl/beep/mp3@latest
//		   go get github.com/gopxl/beep/speaker@latest

// [2026-03-13 12:43] Every Go program NEEDs package main, it will help with having a main function (I learned that too late in my Python project...)
package main

// [2026-03-13 12:44] DO NOT use import for each import individually use parentheses ()

// [2026-03-13 12:46] Don't save in VS after import it wipes the imports.....
import (
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/gopxl/beep"
	"github.com/gopxl/beep/effects"
	"github.com/gopxl/beep/mp3"
	"github.com/gopxl/beep/speaker"
)

var (
	a            fyne.App
	w            fyne.Window
	playPauseBtn *widget.Button
	progress     *widget.Slider
	titleLabel   *widget.Label
	timeLabel    *widget.Label
	volumeSlider *widget.Slider

	currentFile   string
	streamer      beep.Streamer
	format        beep.Format
	ctrl          *beep.Ctrl
	volumeCtrl    *effects.Volume
	playing       bool
	totalDuration time.Duration
	mixer         *beep.Mixer
)

// Lock() & Unlock() allow ONLY one thread to have control, otherwise we might get a crash or audio glitch

func main() {
	a = app.NewWithID("fyne-mp3player")
	a.Settings().SetTheme(theme.DefaultTheme())

	w = a.NewWindow("Music Player")
	w.Resize(fyne.NewSize(500, 220))

	err := speaker.Init(beep.SampleRate(44100), beep.SampleRate(44100).N(time.Second/10))
	if err != nil {
		dialog.ShowError(fmt.Errorf("cannot init audio: %v", err), w)
		return
	}

	mixer = &beep.Mixer{}
	speaker.Play(mixer)

	titleLabel = widget.NewLabel("No track loaded")
	titleLabel.Alignment = fyne.TextAlignCenter
	titleLabel.Wrapping = fyne.TextWrapBreak

	timeLabel = widget.NewLabel("00:00 / 00:00")
	timeLabel.Alignment = fyne.TextAlignCenter

	progress = widget.NewSlider(0, 1)
	progress.Step = 1
	progress.OnChanged = func(p float64) {
		if streamer == nil || totalDuration == 0 {
			return
		}
		pos := time.Duration(p * float64(totalDuration))
		if seeker, ok := streamer.(beep.StreamSeekCloser); ok {
			speaker.Lock()
			_ = seeker.Seek(format.SampleRate.N(pos))
			speaker.Unlock()
		}
	}

	playPauseBtn = widget.NewButtonWithIcon("Play", theme.MediaPlayIcon(), togglePlayPause)
	stopBtn := widget.NewButtonWithIcon("", theme.MediaStopIcon(), stopPlayback)
	openBtn := widget.NewButtonWithIcon("Open file", theme.FolderOpenIcon(), openFileDialog)

	volumeSlider = widget.NewSlider(0, 100)
	volumeSlider.Value = 50.0
	volumeSlider.Step = 5
	volumeLabel := widget.NewLabel("50%")
	volumeSlider.OnChanged = func(v float64) {
		if volumeCtrl != nil {
			speaker.Lock()
			normalized := (v / 50.0) - 1.0
			volumeCtrl.Volume = normalized
			speaker.Unlock()
		}
		volumeLabel.SetText(fmt.Sprintf("%.0f%%", v))
	}

	controls := container.NewHBox(
		openBtn,
		layout.NewSpacer(),
		playPauseBtn,
		stopBtn,
		layout.NewSpacer(),
		widget.NewLabel("Vol: "),
		volumeLabel,
		volumeSlider,
	)

	bottom := container.NewVBox(
		progress,
		container.NewHBox(timeLabel, layout.NewSpacer()),
	)

	content := container.NewVBox(
		canvas.NewRectangle(color.Transparent),
		titleLabel,
		container.NewCenter(container.NewVBox(bottom, controls)),
	)

	w.SetContent(content)
	w.SetOnClosed(stopPlayback)

	// co-routine to update the clock and keep the playhead on "track"
	go func() {
		for range time.Tick(200 * time.Millisecond) {
			if !playing || streamer == nil || totalDuration == 0 {
				continue
			}

			pos := getPosition(streamer)

			length := getLen(streamer)
			if length <= 0 {
				length = 1 // prevent division by zero
			}
			frac := float64(pos) / float64(length)

			cur := format.SampleRate.D(pos).Round(time.Second)
			tot := totalDuration.Round(time.Second)
			timeStr := fmt.Sprintf("%02d:%02d / %02d:%02d",
				int(cur.Minutes()), int(cur.Seconds())%60,
				int(tot.Minutes()), int(tot.Seconds())%60)

			// [2026-03-13 13:50] Had to used this function and move cxode because I was using the wrong thread to update the GUI
			fyne.Do(func() {
				progress.SetValue(frac)
				timeLabel.SetText(timeStr)
			})
		}
	}()

	w.ShowAndRun()
}

// Safe way to get position (returns 0 if not seekable)
func getPosition(s beep.Streamer) int {
	if p, ok := s.(interface{ Position() int }); ok {
		return p.Position()
	}
	return 0
}

// Safe way to get length (returns 1 if not seekable to avoid div-by-zero)
func getLen(s beep.Streamer) int {
	if l, ok := s.(interface{ Len() int }); ok {
		return l.Len()
	}
	return 1
}

func openFileDialog() {
	dialog.ShowFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil || reader == nil {
			return
		}

		stopPlayback()

		currentFile = reader.URI().Path()
		f, err := os.Open(currentFile)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}

		streamer, format, err = mp3.Decode(f)
		if err != nil {
			dialog.ShowError(err, w)
			f.Close()
			return
		}

		targetSR := beep.SampleRate(44100)
		if format.SampleRate != targetSR {
			streamer = beep.Resample(4, format.SampleRate, targetSR, streamer)
			format.SampleRate = targetSR
		}

		totalDuration = format.SampleRate.D(getLen(streamer))

		ctrl = &beep.Ctrl{Streamer: streamer}
		volumeCtrl = &effects.Volume{
			Streamer: ctrl,
			Base:     2,
			Volume:   (volumeSlider.Value / 50.0) - 1.0,
			Silent:   false,
		}

		mixer.Add(volumeCtrl)

		playing = true

		fyne.Do(func() {
			titleLabel.SetText(filepath.Base(currentFile))
			playPauseBtn.SetIcon(theme.MediaPauseIcon())
			playPauseBtn.SetText("Pause")
			progress.SetValue(0)
			progress.Max = 1
		})
	}, w)
}

func togglePlayPause() {
	if streamer == nil {
		return
	}
	speaker.Lock()
	ctrl.Paused = !ctrl.Paused
	speaker.Unlock()

	playing = !ctrl.Paused

	fyne.Do(func() {
		if playing {
			playPauseBtn.SetIcon(theme.MediaPauseIcon())
			playPauseBtn.SetText("Pause")
		} else {
			playPauseBtn.SetIcon(theme.MediaPlayIcon())
			playPauseBtn.SetText("Play")
		}
	})
}

func stopPlayback() {
	speaker.Lock()
	mixer.Clear()
	speaker.Unlock()

	if closer, ok := streamer.(beep.StreamSeekCloser); ok {
		closer.Close()
	}
	streamer = nil

	playing = false
	ctrl = nil
	volumeCtrl = nil
	totalDuration = 0

	fyne.Do(func() {
		playPauseBtn.SetIcon(theme.MediaPlayIcon())
		playPauseBtn.SetText("Play")
		progress.SetValue(0)
		timeLabel.SetText("00:00 / 00:00")
		titleLabel.SetText("No track loaded")
	})
}
