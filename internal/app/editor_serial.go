package app

import (
	"cmp"
	"runtime"
	"strconv"
	"strings"

	"finallyshell/internal/config"
	"finallyshell/internal/serial"
	"finallyshell/internal/widgets"
	"finallyshell/locales"

	"github.com/egoist/mygo/ui"
)

const (
	fieldSerialPort editorField = 400 + iota
	fieldSerialBaud
	fieldSerialStop
)

var serialFieldOrder = []editorField{fieldSerialPort, fieldSerialBaud, fieldSerialStop}

var serialBauds = []string{"300", "1200", "2400", "4800", "9600", "19200", "38400", "57600", "115200", "230400", "460800", "921600"}

type serialFields struct {
	cfg          config.SerialConfig
	device, baud string
	ports        []string
	listed       bool
	err          error
	list         func() ([]string, error)
}

func (a *App) serialFieldsOf(h *config.Host) serialFields {
	f := serialFields{list: a.serialPorts}
	if f.list == nil {
		f.list = serial.Ports
	}
	if h != nil && h.Kind == config.KindSerial {
		f.device = h.Addr
		if h.Serial != nil {
			f.cfg = *h.Serial
		}
		f.refresh(f.list)
	}
	f.baud = strconv.Itoa(cmp.Or(f.cfg.Baud, 9600))
	f.cfg.DataBits = cmp.Or(f.cfg.DataBits, 8)
	f.cfg.StopBits = cmp.Or(f.cfg.StopBits, 1)
	f.cfg.Parity = cmp.Or(f.cfg.Parity, "none")
	f.cfg.Flow = cmp.Or(f.cfg.Flow, "none")
	f.cfg.LineEnding = cmp.Or(f.cfg.LineEnding, "cr")
	return f
}

func (f *serialFields) refresh(list func() ([]string, error)) {
	if list != nil {
		f.ports, f.err = list()
		f.listed = true
	}
}

func (ed *connEditor) serialProblems(errs map[editorField]string) {
	f := &ed.serial
	if !serial.ValidPort(runtime.GOOS, strings.TrimSpace(f.device)) {
		errs[fieldSerialPort] = locales.SerialDeviceInvalid()
	}
	baud, err := strconv.Atoi(strings.TrimSpace(f.baud))
	if err != nil || baud < 1 || baud > 100_000_000 {
		errs[fieldSerialBaud] = locales.SerialBaudInvalid()
	}
	if serial.Validate(runtime.GOOS, f.cfg) != nil {
		errs[fieldSerialStop] = locales.SerialStopBitsInvalid()
	}
}

func (a *App) saveSerial(h *config.Host, ed *connEditor) {
	a.saveTerminalOnly(h, ed)
	h.Addr, h.Port, h.User, h.TermType = strings.TrimSpace(ed.serial.device), 0, "", ""
	h.Auth = config.AuthPassword
	a.keepLogin(h, "", false, false)
	options := ed.serial.cfg
	options.Baud, _ = strconv.Atoi(strings.TrimSpace(ed.serial.baud))
	h.Serial = &options
}

func (a *App) serialPages(ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) []widgets.NavPage {
	return []widgets.NavPage{
		{ID: editorSSH, Title: locales.SerialKind, View: func(c *ui.Context) { serialPage(c, ed, errs, input) }},
		{ID: editorTerminal, Title: locales.EditorPageTerminal, View: func(c *ui.Context) { a.terminalPage(c, ed, errs, input) }},
	}
}

func serialPage(c *ui.Context, ed *connEditor, errs map[editorField]string, input func(ui.Element) ui.Element) {
	f := &ed.serial
	ui.Form(c, func() {
		ui.Fieldset(c, locales.SectionGeneral(), func() {
			kindField(c, ed)
			ui.Field(c, locales.FieldName(), func() { input(ui.TextInput(c, &ed.name).Placeholder(locales.FieldNamePlaceholder())) })
			ui.Field(c, locales.SerialDevice(), func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					input(ui.Autocomplete(c, &f.device, f.ports).Grow(1).MinWidth(0).Label(locales.SerialDevice()))
					if widgets.IconButton(c, "refresh-cw", locales.SerialRefresh()).Clicked() {
						f.refresh(f.list)
					}
				})
			}).Error(errs[fieldSerialPort]).Description(serialPortNote(f))
			ui.Field(c, locales.FieldNotes(), func() { ui.TextArea(c, &ed.notes).Lines(2, 4) })
		})
		ui.Fieldset(c, locales.SerialSettings(), func() {
			ui.Field(c, locales.SerialBaud(), func() { input(ui.Autocomplete(c, &f.baud, serialBauds).Width(135).Label(locales.SerialBaud())) }).Error(errs[fieldSerialBaud])
			ui.Field(c, locales.SerialDataBits(), func() {
				rdpSelect(c, locales.SerialDataBits(), &f.cfg.DataBits, []int{5, 6, 7, 8}, []string{"5", "6", "7", "8"}, 135)
			})
			ui.Field(c, locales.SerialParity(), func() {
				rdpSelect(c, locales.SerialParity(), &f.cfg.Parity, []string{"none", "even", "odd"},
					[]string{locales.SerialParityNone(), locales.SerialParityEven(), locales.SerialParityOdd()}, 135)
			})
			ui.Field(c, locales.SerialStopBits(), func() {
				values, labels := []float64{1, 2}, []string{"1", "2"}
				if runtime.GOOS == "windows" || f.cfg.StopBits == 1.5 {
					values, labels = []float64{1, 1.5, 2}, []string{"1", "1.5", "2"}
				}
				rdpSelect(c, locales.SerialStopBits(), &f.cfg.StopBits, values, labels, 135)
			}).Error(errs[fieldSerialStop])
			ui.Field(c, locales.SerialFlow(), func() {
				rdpSelect(c, locales.SerialFlow(), &f.cfg.Flow, []string{"none", "rtscts", "xonxoff"}, []string{locales.SerialParityNone(), "RTS/CTS", "XON/XOFF"}, 135)
			})
			ui.Field(c, locales.SerialLineEnding(), func() {
				rdpSelect(c, locales.SerialLineEnding(), &f.cfg.LineEnding, []string{"cr", "lf", "crlf"}, []string{"CR", "LF", "CRLF"}, 135)
			})
			ui.Field(c, "", func() { ui.Checkbox(c, &f.cfg.LocalEcho, locales.SerialLocalEcho()) })
		})
	})
}

func serialPortNote(f *serialFields) string {
	if f.err != nil {
		return locales.SerialPortsFailed(f.err.Error())
	}
	if len(f.ports) == 0 {
		return locales.SerialNoPorts()
	}
	return ""
}
